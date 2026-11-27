package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CancellationWorkerConfig configures the event cancellation background worker.
type CancellationWorkerConfig struct {
	PollInterval   time.Duration
	BatchSize      int32
	LeaseDuration  time.Duration
	BaseRetryDelay time.Duration
	MaxRetryDelay  time.Duration
	MaxRetries     int
}

func DefaultCancellationWorkerConfig() CancellationWorkerConfig {
	return CancellationWorkerConfig{
		PollInterval:   1 * time.Second,
		BatchSize:      50,
		LeaseDuration:  2 * time.Minute,
		BaseRetryDelay: 5 * time.Second,
		MaxRetryDelay:  15 * time.Minute,
		MaxRetries:     5,
	}
}

// CancellationWorker processes event.cancellation_requested outbox events,
// invalidating held reservations and cancelling confirmed bookings in batches.
type CancellationWorker struct {
	pool       *pgxpool.Pool
	outboxRepo outbox.Repository
	config     CancellationWorkerConfig
	logger     *slog.Logger
}

func NewCancellationWorker(
	pool *pgxpool.Pool,
	outboxRepo outbox.Repository,
	cfg CancellationWorkerConfig,
	logger *slog.Logger,
) (*CancellationWorker, error) {
	if pool == nil {
		return nil, errors.New("cancellation_worker: database pool cannot be nil")
	}
	if outboxRepo == nil {
		return nil, errors.New("cancellation_worker: outbox repository cannot be nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 2 * time.Minute
	}
	if cfg.BaseRetryDelay <= 0 {
		cfg.BaseRetryDelay = 5 * time.Second
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = 15 * time.Minute
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &CancellationWorker{
		pool:       pool,
		outboxRepo: outboxRepo,
		config:     cfg,
		logger:     logger,
	}, nil
}

// Run executes the worker polling loop until ctx is cancelled.
func (w *CancellationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	w.logger.Info("event cancellation worker running",
		"poll_interval", w.config.PollInterval,
		"batch_size", w.config.BatchSize,
		"lease_duration", w.config.LeaseDuration,
	)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("event cancellation worker stopping gracefully")
			return nil
		case <-ticker.C:
			for {
				count, err := w.ProcessNextBatch(ctx)
				if err != nil {
					if errors.Is(err, context.Canceled) || ctx.Err() != nil {
						return nil
					}
					w.logger.Error("error processing cancellation outbox batch", "error", err)
					break
				}
				if count == 0 || ctx.Err() != nil {
					break
				}
			}
		}
	}
}

// ProcessNextBatch claims and processes up to 1 cancellation event.
func (w *CancellationWorker) ProcessNextBatch(ctx context.Context) (int, error) {
	events, err := w.outboxRepo.ClaimEligibleEvents(
		ctx,
		[]string{EventTypeEventCancellationRequested},
		1,
		w.config.LeaseDuration,
	)
	if err != nil {
		return 0, fmt.Errorf("claiming cancellation outbox events: %w", err)
	}

	if len(events) == 0 {
		return 0, nil
	}

	for _, event := range events {
		if ctx.Err() != nil {
			return len(events), ctx.Err()
		}
		w.ProcessEvent(ctx, event)
	}

	return len(events), nil
}

// ProcessEvent executes downstream cancellation batching for an event.
func (w *CancellationWorker) ProcessEvent(ctx context.Context, evt sqlc.OutboxEvent) {
	eventIDStr := uuid.UUID(evt.ID.Bytes).String()

	var payload EventCancellationPayload
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		w.logger.Error("invalid event cancellation payload", "outbox_id", eventIDStr, "error", err)
		_, _ = w.outboxRepo.FailEvent(ctx, evt.ID, evt.ClaimToken, false, fmt.Sprintf("invalid payload: %v", err))
		return
	}

	eventUUID, err := uuid.Parse(payload.EventID)
	if err != nil {
		w.logger.Error("invalid event ID in cancellation payload", "outbox_id", eventIDStr, "payload_event_id", payload.EventID, "error", err)
		_, _ = w.outboxRepo.FailEvent(ctx, evt.ID, evt.ClaimToken, false, fmt.Sprintf("invalid event ID: %v", err))
		return
	}
	eventDBID := pgtype.UUID{Bytes: eventUUID, Valid: true}

	// 1. Invalidate held reservations in batches until none remain
	for {
		if ctx.Err() != nil {
			return
		}

		released, err := w.invalidateHeldBatch(ctx, eventDBID, payload.Reason)
		if err != nil {
			w.logger.Error("failed to invalidate held reservations batch", "event_id", payload.EventID, "error", err)
			w.scheduleRetry(ctx, evt, err)
			return
		}
		if len(released) == 0 {
			break
		}
	}

	// 2. Cancel confirmed bookings in batches & enqueue refunds until none remain
	for {
		if ctx.Err() != nil {
			return
		}

		processed, err := w.cancelBookingsBatch(ctx, eventDBID)
		if err != nil {
			w.logger.Error("failed to cancel confirmed bookings batch", "event_id", payload.EventID, "error", err)
			w.scheduleRetry(ctx, evt, err)
			return
		}
		if processed == 0 {
			break
		}
	}

	// 3. Mark outbox event published / complete
	_, err = w.outboxRepo.CompleteEvent(ctx, evt.ID, evt.ClaimToken, "")
	if err != nil {
		w.logger.Error("failed to complete cancellation outbox event", "outbox_id", eventIDStr, "event_id", payload.EventID, "error", err)
		return
	}

	w.logger.Info("event cancellation downstream processing completed", "event_id", payload.EventID)
}

func (w *CancellationWorker) invalidateHeldBatch(
	ctx context.Context,
	eventID pgtype.UUID,
	reason string,
) ([]sqlc.InvalidateHeldReservationsForEventBatchRow, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	q := sqlc.New(tx)
	released, err := q.InvalidateHeldReservationsForEventBatch(ctx, sqlc.InvalidateHeldReservationsForEventBatchParams{
		EventID:            eventID,
		LimitCount:         w.config.BatchSize,
		CancellationReason: pgtype.Text{String: reason, Valid: reason != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("invalidate reservations query: %w", err)
	}

	for _, row := range released {
		if row.ReleasedQuantity > 0 {
			_, err := q.DecrementReservedInventory(ctx, sqlc.DecrementReservedInventoryParams{
				TicketCategoryID: row.TicketCategoryID,
				Quantity:         row.ReleasedQuantity,
			})
			if err != nil {
				return nil, fmt.Errorf("decrement reserved inventory for category %s: %w", uuid.UUID(row.TicketCategoryID.Bytes).String(), err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch transaction: %w", err)
	}

	return released, nil
}

func (w *CancellationWorker) cancelBookingsBatch(
	ctx context.Context,
	eventID pgtype.UUID,
) (int, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	q := sqlc.New(tx)
	bookings, err := q.GetConfirmedBookingsForEventBatch(ctx, sqlc.GetConfirmedBookingsForEventBatchParams{
		EventID:    eventID,
		LimitCount: w.config.BatchSize,
	})
	if err != nil {
		return 0, fmt.Errorf("query confirmed bookings: %w", err)
	}

	if len(bookings) == 0 {
		_ = tx.Rollback(ctx)
		return 0, nil
	}

	bookingIDs := make([]pgtype.UUID, len(bookings))
	for i, b := range bookings {
		bookingIDs[i] = b.ID
	}

	_, err = q.CancelBookingsBatch(ctx, bookingIDs)
	if err != nil {
		return 0, fmt.Errorf("cancel bookings query: %w", err)
	}

	for _, b := range bookings {
		if b.PaymentAttemptID.Valid {
			refundUUID := uuid.New()
			idempotencyKey := fmt.Sprintf("event_cancel_%s", uuid.UUID(b.ID.Bytes).String())
			_, err := q.CreateRefund(ctx, sqlc.CreateRefundParams{
				ID:               pgtype.UUID{Bytes: refundUUID, Valid: true},
				PaymentAttemptID: b.PaymentAttemptID,
				BookingID:        b.ID,
				AmountMinor:      b.TotalPriceMinor,
				Currency:         b.Currency,
				IdempotencyKey:   idempotencyKey,
			})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return 0, fmt.Errorf("create refund for booking %s: %w", uuid.UUID(b.ID.Bytes).String(), err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit bookings batch: %w", err)
	}

	return len(bookings), nil
}

func (w *CancellationWorker) scheduleRetry(ctx context.Context, evt sqlc.OutboxEvent, err error) {
	if int(evt.Attempts)+1 >= w.config.MaxRetries {
		w.logger.Error("max retries exceeded for cancellation outbox event",
			"outbox_id", uuid.UUID(evt.ID.Bytes).String(),
			"attempts", evt.Attempts+1,
		)
		_, _ = w.outboxRepo.FailEvent(ctx, evt.ID, evt.ClaimToken, true, err.Error())
		return
	}

	delay := w.config.BaseRetryDelay * time.Duration(1<<evt.Attempts)
	if delay > w.config.MaxRetryDelay {
		delay = w.config.MaxRetryDelay
	}
	nextAttempt := time.Now().UTC().Add(delay)

	w.logger.Warn("rescheduling cancellation outbox event",
		"outbox_id", uuid.UUID(evt.ID.Bytes).String(),
		"next_attempt_at", nextAttempt,
		"error", err,
	)
	_, _ = w.outboxRepo.RetryEvent(ctx, evt.ID, evt.ClaimToken, nextAttempt, err.Error())
}

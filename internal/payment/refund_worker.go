package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrPermanentRefundFailure indicates a non-retryable refund failure from the provider.
var ErrPermanentRefundFailure = errors.New("payment: permanent refund failure")

// RefundWorkerConfig holds configuration for the background refund worker.
type RefundWorkerConfig struct {
	PollInterval   time.Duration
	BatchSize      int32
	LeaseDuration  time.Duration
	BaseRetryDelay time.Duration
	MaxRetryDelay  time.Duration
	MaxRetries     int
}

func DefaultRefundWorkerConfig() RefundWorkerConfig {
	return RefundWorkerConfig{
		PollInterval:   1 * time.Second,
		BatchSize:      10,
		LeaseDuration:  2 * time.Minute,
		BaseRetryDelay: 5 * time.Second,
		MaxRetryDelay:  15 * time.Minute,
		MaxRetries:     5,
	}
}

// RefundWorker claims pending refunds and executes them against the configured Provider.
type RefundWorker struct {
	repo     *Repository
	provider Provider
	config   RefundWorkerConfig
	logger   *slog.Logger
}

func NewRefundWorker(repo *Repository, provider Provider, cfg RefundWorkerConfig, logger *slog.Logger) (*RefundWorker, error) {
	if repo == nil {
		return nil, errors.New("refund_worker: repository cannot be nil")
	}
	if provider == nil {
		return nil, errors.New("refund_worker: provider cannot be nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
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

	return &RefundWorker{
		repo:     repo,
		provider: provider,
		config:   cfg,
		logger:   logger,
	}, nil
}

// Run executes the refund polling loop until ctx is cancelled.
func (w *RefundWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	w.logger.Info("refund worker running",
		"poll_interval", w.config.PollInterval,
		"batch_size", w.config.BatchSize,
		"lease_duration", w.config.LeaseDuration,
	)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("refund worker stopping gracefully")
			return nil
		case <-ticker.C:
			for {
				count, err := w.ProcessNextBatch(ctx)
				if err != nil {
					if errors.Is(err, context.Canceled) || ctx.Err() != nil {
						return nil
					}
					w.logger.Error("error processing refund batch", "error", err)
					break
				}
				if count < int(w.config.BatchSize) || ctx.Err() != nil {
					break
				}
			}
		}
	}
}

// ProcessNextBatch claims and processes up to BatchSize eligible refunds.
func (w *RefundWorker) ProcessNextBatch(ctx context.Context) (int, error) {
	lease := pgtype.Interval{
		Microseconds: int64(w.config.LeaseDuration / time.Microsecond),
		Valid:        true,
	}

	refunds, err := w.repo.ClaimPendingRefunds(ctx, w.config.BatchSize, lease)
	if err != nil {
		return 0, fmt.Errorf("claiming refunds: %w", err)
	}
	if len(refunds) == 0 {
		return 0, nil
	}

	for _, refund := range refunds {
		if ctx.Err() != nil {
			return len(refunds), ctx.Err()
		}
		w.ProcessRefund(ctx, refund)
	}

	return len(refunds), nil
}

// ProcessRefund executes a single claimed refund against the payment provider.
func (w *RefundWorker) ProcessRefund(ctx context.Context, refund sqlc.Refund) {
	refundIDStr := uuid.UUID(refund.ID.Bytes).String()

	attempt, err := w.repo.GetPaymentAttemptByID(ctx, refund.PaymentAttemptID)
	if err != nil {
		w.logger.Error("failed to load parent payment attempt for refund", "refund_id", refundIDStr, "error", err)
		_, _ = w.repo.FailRefund(ctx, refund.ID, refund.ClaimToken, false, fmt.Sprintf("load payment attempt: %v", err))
		return
	}

	if attempt.Status != "succeeded" {
		w.logger.Error("cannot refund non-succeeded payment attempt", "refund_id", refundIDStr, "attempt_status", attempt.Status)
		_, _ = w.repo.FailRefund(ctx, refund.ID, refund.ClaimToken, false, fmt.Sprintf("payment attempt status is %s, expected succeeded", attempt.Status))
		return
	}

	res, refundErr := w.provider.Refund(ctx, RefundRequest{
		RefundID:                 refundIDStr,
		PaymentAttemptID:         uuid.UUID(attempt.ID.Bytes).String(),
		ProviderPaymentReference: attempt.ProviderReference.String,
		ProviderOrderReference:   attempt.ProviderOrderReference.String,
		AmountMinor:              refund.AmountMinor,
		Currency:                 refund.Currency,
	})
	if refundErr == nil {
		updated, err := w.repo.CompleteRefund(ctx, refund.ID, refund.ClaimToken, res.ProviderRefundReference)
		if err != nil {
			w.logger.Error("failed to mark refund succeeded", "refund_id", refundIDStr, "error", err)
			return
		}
		if !updated {
			w.logger.Warn("stale refund worker fenced out on completion", "refund_id", refundIDStr)
			return
		}
		w.logger.Info("refund completed", "refund_id", refundIDStr, "provider_refund_reference", res.ProviderRefundReference)
		return
	}

	currentAttempts := int(refund.Attempts) + 1
	isPermanent := errors.Is(refundErr, ErrPermanentRefundFailure)
	if !isPermanent && currentAttempts < w.config.MaxRetries {
		backoff := calculateRefundBackoff(currentAttempts, w.config.BaseRetryDelay, w.config.MaxRetryDelay)
		nextAttempt := pgtype.Timestamptz{Time: time.Now().UTC().Add(backoff), Valid: true}
		w.logger.Warn("transient refund failure, scheduling retry",
			"refund_id", refundIDStr,
			"attempt", currentAttempts,
			"backoff", backoff,
			"error", refundErr.Error(),
		)
		updated, err := w.repo.RetryRefund(ctx, refund.ID, refund.ClaimToken, nextAttempt, refundErr.Error())
		if err != nil {
			w.logger.Error("failed to schedule refund retry", "refund_id", refundIDStr, "error", err)
			return
		}
		if !updated {
			w.logger.Warn("stale refund worker fenced out on retry", "refund_id", refundIDStr)
		}
		return
	}

	errMsg := refundErr.Error()
	if !isPermanent {
		errMsg = fmt.Sprintf("max retries (%d) exceeded: %s", w.config.MaxRetries, errMsg)
	}
	w.logger.Error("refund failed permanently", "refund_id", refundIDStr, "attempts", currentAttempts, "error", errMsg)
	updated, err := w.repo.FailRefund(ctx, refund.ID, refund.ClaimToken, true, errMsg)
	if err != nil {
		w.logger.Error("failed to mark refund failed", "refund_id", refundIDStr, "error", err)
		return
	}
	if !updated {
		w.logger.Warn("stale refund worker fenced out on failure", "refund_id", refundIDStr)
	}
}

func calculateRefundBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	delay := baseDelay
	for i := 1; i < attempt; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

// RetryFailedRefund transitions a failed refund back to pending for immediate worker pickup.
func (s *Service) RetryFailedRefund(ctx context.Context, refundIDStr string) (sqlc.Refund, error) {
	refundUUID, err := uuid.Parse(refundIDStr)
	if err != nil {
		return sqlc.Refund{}, fmt.Errorf("payment: invalid refund id: %w", err)
	}
	return s.repository.RetryFailedRefund(ctx, pgtype.UUID{Bytes: refundUUID, Valid: true})
}

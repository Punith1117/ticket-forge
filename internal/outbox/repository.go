package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the database operations for claiming, updating, and inserting outbox events.
type Repository interface {
	ClaimEligibleEvents(ctx context.Context, supportedEventTypes []string, limit int32, leaseDuration time.Duration) ([]sqlc.OutboxEvent, error)
	CompleteEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, providerMsgID string) (bool, error)
	RetryEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, nextAttemptAt time.Time, lastError string) (bool, error)
	FailEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error)
	InsertEvent(ctx context.Context, aggregateType string, aggregateID pgtype.UUID, eventType string, payload []byte) (sqlc.OutboxEvent, error)
}

// PGRepository implements Repository using PostgreSQL and sqlc.Queries.
type PGRepository struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

// NewPGRepository constructs a PGRepository.
func NewPGRepository(pool *pgxpool.Pool) *PGRepository {
	return &PGRepository{
		pool:    pool,
		queries: sqlc.New(pool),
	}
}

// ClaimEligibleEvents atomically claims up to limit pending or lease-expired events matching
// supportedEventTypes using SELECT ... FOR UPDATE SKIP LOCKED.
func (r *PGRepository) ClaimEligibleEvents(ctx context.Context, supportedEventTypes []string, limit int32, leaseDuration time.Duration) ([]sqlc.OutboxEvent, error) {
	if len(supportedEventTypes) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if leaseDuration <= 0 {
		leaseDuration = 2 * time.Minute
	}

	interval := pgtype.Interval{
		Microseconds: leaseDuration.Microseconds(),
		Valid:        true,
	}

	return r.queries.ClaimOutboxEvents(ctx, sqlc.ClaimOutboxEventsParams{
		LeaseDuration: interval,
		EventTypes:    supportedEventTypes,
		LimitCount:    limit,
	})
}

// CompleteEvent marks an event as published and scrubs encrypted token material from its payload,
// conditional on id, status = 'processing', and exact claim_token. Returns true if updated.
func (r *PGRepository) CompleteEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, providerMsgID string) (bool, error) {
	var pMsgID pgtype.Text
	if providerMsgID != "" {
		pMsgID = pgtype.Text{String: providerMsgID, Valid: true}
	}

	rowsAffected, err := r.queries.CompleteOutboxEvent(ctx, sqlc.CompleteOutboxEventParams{
		ID:                id,
		ClaimToken:        claimToken,
		ProviderMessageID: pMsgID,
	})
	if err != nil {
		return false, fmt.Errorf("outbox: failed to complete event: %w", err)
	}

	return rowsAffected > 0, nil
}

// RetryEvent schedules an event for retry with exponential backoff and increments attempts,
// conditional on id, status = 'processing', and exact claim_token. Returns true if updated.
func (r *PGRepository) RetryEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, nextAttemptAt time.Time, lastError string) (bool, error) {
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}

	rowsAffected, err := r.queries.RetryOutboxEvent(ctx, sqlc.RetryOutboxEventParams{
		ID:            id,
		ClaimToken:    claimToken,
		NextAttemptAt: pgtype.Timestamptz{Time: nextAttemptAt.UTC(), Valid: true},
		LastError:     pgtype.Text{String: lastError, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("outbox: failed to retry event: %w", err)
	}

	return rowsAffected > 0, nil
}

// FailEvent permanently fails an event and scrubs encrypted token material from its payload,
// conditional on id, status = 'processing', and exact claim_token. Returns true if updated.
func (r *PGRepository) FailEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}

	rowsAffected, err := r.queries.FailOutboxEvent(ctx, sqlc.FailOutboxEventParams{
		ID:                id,
		ClaimToken:        claimToken,
		DeliveryAttempted: deliveryAttempted,
		LastError:         pgtype.Text{String: lastError, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("outbox: failed to mark event as failed: %w", err)
	}

	return rowsAffected > 0, nil
}

// InsertEvent records a new outbox event in the database.
func (r *PGRepository) InsertEvent(ctx context.Context, aggregateType string, aggregateID pgtype.UUID, eventType string, payload []byte) (sqlc.OutboxEvent, error) {
	return r.queries.InsertOutboxEvent(ctx, sqlc.InsertOutboxEventParams{
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		Payload:       payload,
	})
}

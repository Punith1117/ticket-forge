package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresLimiter implements the Limiter and Cleaner interfaces using PostgreSQL
// as the authoritative persistent data store.
type PostgresLimiter struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

// NewPostgresLimiter creates a new PostgresLimiter with the given connection pool.
func NewPostgresLimiter(pool *pgxpool.Pool) (*PostgresLimiter, error) {
	if pool == nil {
		return nil, errors.New("ratelimit: database pool cannot be nil")
	}
	return &PostgresLimiter{
		pool:    pool,
		queries: sqlc.New(pool),
	}, nil
}

// Allow atomically evaluates the rate limit for key against policy using a PostgreSQL transaction
// and row-level locking.
func (l *PostgresLimiter) Allow(ctx context.Context, key string, policy Policy) (Result, error) {
	if err := ValidateKey(key); err != nil {
		return Result{}, err
	}
	if err := policy.Validate(); err != nil {
		return Result{}, err
	}

	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := l.queries.WithTx(tx)

	// 1. Ensure the bucket row exists safely under concurrency using INSERT ... ON CONFLICT DO NOTHING.
	if err := qtx.InsertBucketIfNotExists(ctx, sqlc.InsertBucketIfNotExistsParams{
		Key:      key,
		Capacity: policy.Capacity,
	}); err != nil {
		return Result{}, fmt.Errorf("ratelimit: failed to initialize bucket: %w", err)
	}

	// 2. Lock and retrieve the bucket row, obtaining authoritative database clock_timestamp().
	bucket, err := qtx.GetRateLimitBucketForUpdate(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: failed to lock bucket: %w", err)
	}

	dbTime := bucket.DbTime.Time
	lastRefill := bucket.LastRefillAt.Time

	// 3. Calculate elapsed microseconds since last refill.
	elapsedMicros := dbTime.Sub(lastRefill).Microseconds()
	if elapsedMicros < 0 {
		elapsedMicros = 0
	}

	refillIntervalMicros := policy.RefillInterval.Microseconds()
	if refillIntervalMicros <= 0 {
		refillIntervalMicros = 1
	}

	// 4. Calculate replenished integer tokens: (elapsed * refillTokens) / interval
	tokensToAdd := (elapsedMicros * policy.RefillTokens) / refillIntervalMicros
	avail := bucket.Tokens + tokensToAdd
	if avail > policy.Capacity {
		avail = policy.Capacity
	}

	var (
		allowed       bool
		remaining     int64
		retryAfter    time.Duration
		newLastRefill time.Time
	)

	// 5. Evaluate token consumption.
	if avail >= 1 {
		allowed = true
		remaining = avail - 1
		retryAfter = 0

		if bucket.Tokens+tokensToAdd >= policy.Capacity {
			// Capacity ceiling reached: reset refill clock to dbTime so excess time is capped.
			newLastRefill = dbTime
		} else {
			// Advance lastRefill ONLY by the exact time consumed by the integer tokens added.
			// This preserves the fractional remainder without losing elapsed-time progress.
			timeUsedMicros := (tokensToAdd * refillIntervalMicros) / policy.RefillTokens
			newLastRefill = lastRefill.Add(time.Duration(timeUsedMicros) * time.Microsecond)
		}
	} else {
		allowed = false
		remaining = 0

		// Time until the NEXT token becomes available:
		// ceil(refillIntervalMicros / refillTokens) = (interval + tokens - 1) / tokens
		timeForOneTokenMicros := (refillIntervalMicros + policy.RefillTokens - 1) / policy.RefillTokens
		if timeForOneTokenMicros > elapsedMicros {
			retryAfter = time.Duration(timeForOneTokenMicros-elapsedMicros) * time.Microsecond
		} else {
			retryAfter = time.Microsecond
		}

		// Do NOT advance lastRefill on denial; preserve accumulated elapsed-time progress.
		newLastRefill = lastRefill
	}

	// 6. Persist new balance and timestamps.
	if _, err := qtx.UpdateRateLimitBucket(ctx, sqlc.UpdateRateLimitBucketParams{
		Key:          key,
		Tokens:       remaining,
		LastRefillAt: pgtype.Timestamptz{Time: newLastRefill, Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: dbTime, Valid: true},
	}); err != nil {
		return Result{}, fmt.Errorf("ratelimit: failed to update bucket: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("ratelimit: failed to commit rate limit transaction: %w", err)
	}

	return Result{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: retryAfter,
	}, nil
}

// Cleanup removes inactive rate-limiting buckets whose updated_at timestamp is older than retention.
// Returns the number of deleted rows.
func (l *PostgresLimiter) Cleanup(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	if retention <= 0 {
		return 0, errors.New("ratelimit: retention duration must be positive")
	}
	if limit <= 0 {
		limit = DefaultCleanupBatchSize
	}

	cutoff := time.Now().UTC().Add(-retention)
	rows, err := l.queries.DeleteInactiveBuckets(ctx, sqlc.DeleteInactiveBucketsParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff, Valid: true},
		LimitCount: int64(limit),
	})
	if err != nil {
		return 0, fmt.Errorf("ratelimit: failed to cleanup inactive buckets: %w", err)
	}
	return rows, nil
}

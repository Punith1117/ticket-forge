package booking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpirationWorkerConfig holds the configuration for the reservation expiration background worker.
type ExpirationWorkerConfig struct {
	Enabled           bool
	PollInterval      time.Duration
	BatchSize         int32
	MaxBatchesPerTick int
	ShutdownTimeout   time.Duration
}

// DefaultExpirationWorkerConfig provides default values aligned with application settings.
func DefaultExpirationWorkerConfig() ExpirationWorkerConfig {
	return ExpirationWorkerConfig{
		Enabled:           true,
		PollInterval:      15 * time.Second,
		BatchSize:         100,
		MaxBatchesPerTick: 10,
		ShutdownTimeout:   30 * time.Second,
	}
}

// ExpirationWorkerConfigFromEnv reads configuration from the environment, falling back to defaults.
func ExpirationWorkerConfigFromEnv() ExpirationWorkerConfig {
	cfg := DefaultExpirationWorkerConfig()

	if val := os.Getenv("RESERVATION_EXPIRATION_WORKER_ENABLED"); val != "" {
		cfg.Enabled = parseBool(val, true)
	}

	if val := os.Getenv("RESERVATION_EXPIRATION_POLL_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			cfg.PollInterval = d
		}
	}

	if val := os.Getenv("RESERVATION_EXPIRATION_BATCH_SIZE"); val != "" {
		if n, err := strconv.ParseInt(val, 10, 32); err == nil && n > 0 {
			cfg.BatchSize = int32(n)
		}
	}

	if val := os.Getenv("RESERVATION_EXPIRATION_MAX_BATCHES_PER_TICK"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MaxBatchesPerTick = n
		}
	}

	if val := os.Getenv("RESERVATION_EXPIRATION_WORKER_SHUTDOWN_TIMEOUT"); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			cfg.ShutdownTimeout = d
		}
	}

	return cfg
}

func parseBool(v string, defaultVal bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return defaultVal
	}
}

// Validate verifies that the worker configuration has positive values.
func (c *ExpirationWorkerConfig) Validate() error {
	if c.PollInterval <= 0 {
		return errors.New("reservation_expiration: poll interval must be positive")
	}
	if c.BatchSize <= 0 {
		return errors.New("reservation_expiration: batch size must be positive")
	}
	if c.MaxBatchesPerTick <= 0 {
		return errors.New("reservation_expiration: max batches per tick must be positive")
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("reservation_expiration: shutdown timeout must be positive")
	}
	return nil
}

// ExpirationWorker periodically claims and expires held reservations that have passed
// their expiration timestamp, releasing their reserved inventory atomically.
type ExpirationWorker struct {
	pool   *pgxpool.Pool
	repo   *Repository
	config ExpirationWorkerConfig
	logger *slog.Logger
}

// NewExpirationWorker creates a new ExpirationWorker instance.
func NewExpirationWorker(
	pool *pgxpool.Pool,
	cfg ExpirationWorkerConfig,
	logger *slog.Logger,
) (*ExpirationWorker, error) {
	if pool == nil {
		return nil, errors.New("reservation_expiration: database pool cannot be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	repo, err := NewRepository(pool)
	if err != nil {
		return nil, fmt.Errorf("reservation_expiration: init repository: %w", err)
	}

	return &ExpirationWorker{
		pool:   pool,
		repo:   repo,
		config: cfg,
		logger: logger,
	}, nil
}

// Run starts the expiration worker loop and runs until ctx is cancelled.
func (w *ExpirationWorker) Run(ctx context.Context) error {
	if !w.config.Enabled {
		w.logger.Info("reservation expiration worker is disabled")
		<-ctx.Done()
		return nil
	}

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	w.logger.Info("reservation expiration worker running",
		"poll_interval", w.config.PollInterval,
		"batch_size", w.config.BatchSize,
		"max_batches_per_tick", w.config.MaxBatchesPerTick,
		"shutdown_timeout", w.config.ShutdownTimeout,
	)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("reservation expiration worker stopping gracefully")
			return nil
		case <-ticker.C:
			w.runTick(ctx)
		}
	}
}

// runTick processes up to MaxBatchesPerTick batches in a single tick.
func (w *ExpirationWorker) runTick(ctx context.Context) {
	batchesProcessed := 0
	totalExpired := 0

	for batchesProcessed < w.config.MaxBatchesPerTick {
		if ctx.Err() != nil {
			return
		}

		expiredCount, err := w.ProcessNextBatch(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			w.logger.Error("error processing reservation expiration batch", "error", err)
			break
		}

		totalExpired += expiredCount
		batchesProcessed++

		// If fewer than BatchSize items were expired, there are no more candidates in this tick.
		if expiredCount < int(w.config.BatchSize) {
			break
		}
	}

	if totalExpired > 0 {
		w.logger.Info("reservation expiration tick completed",
			"batches", batchesProcessed,
			"expired_reservations", totalExpired,
		)
	}
}

// ProcessNextBatch claims a single batch of expired held reservations using SKIP LOCKED,
// marks them expired, and releases their reserved inventory within an atomic transaction.
// Returns the total number of reservations expired in this batch.
func (w *ExpirationWorker) ProcessNextBatch(ctx context.Context) (int, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("reservation_expiration: begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	results, err := w.repo.ExpireHeldReservationsBatch(ctx, tx, w.config.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("reservation_expiration: expire batch: %w", err)
	}

	if len(results) == 0 {
		_ = tx.Rollback(ctx)
		return 0, nil
	}

	totalExpiredInBatch := 0
	for _, row := range results {
		totalExpiredInBatch += int(row.ExpiredCount)
		if row.ReleasedQuantity > 0 {
			rowsAffected, err := w.repo.DecrementReservedInventory(ctx, tx, row.TicketCategoryID, row.ReleasedQuantity)
			if err != nil {
				return 0, fmt.Errorf("reservation_expiration: decrement reserved inventory: %w", err)
			}
			if rowsAffected != 1 {
				return 0, fmt.Errorf(
					"reservation_expiration: decrement reserved inventory affected %d rows for ticket category %v (expected 1)",
					rowsAffected,
					row.TicketCategoryID,
				)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("reservation_expiration: commit transaction: %w", err)
	}

	return totalExpiredInBatch, nil
}

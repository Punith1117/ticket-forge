package outbox

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// WorkerConfig holds the configuration for the background email outbox worker.
type WorkerConfig struct {
	PollInterval    time.Duration
	BatchSize       int32
	LeaseDuration   time.Duration
	BaseRetryDelay  time.Duration
	MaxRetryDelay   time.Duration
	MaxRetries      int
	BaseURL         string
	From            string
	SupportedEvents []string
}

// WorkerConfigFromEnv reads outbox worker settings from environment variables.
func WorkerConfigFromEnv() WorkerConfig {
	pollInterval := 1 * time.Second
	if val := os.Getenv("WORKER_POLL_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			pollInterval = d
		}
	}

	batchSize := int32(10)
	if val := os.Getenv("WORKER_BATCH_SIZE"); val != "" {
		if n, err := strconv.ParseInt(val, 10, 32); err == nil && n > 0 {
			batchSize = int32(n)
		}
	}

	leaseDuration := 2 * time.Minute
	if val := os.Getenv("WORKER_LEASE_DURATION"); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			leaseDuration = d
		}
	}

	maxRetries := 5
	if val := os.Getenv("WORKER_MAX_RETRIES"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			maxRetries = n
		}
	}

	baseURL := os.Getenv("APP_BASE_URL")
	if baseURL == "" {
		baseURL = getEnv("BASE_URL", "http://localhost:8080")
	}

	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = "noreply@ticketforge.local"
	}

	return WorkerConfig{
		PollInterval:    pollInterval,
		BatchSize:       batchSize,
		LeaseDuration:   leaseDuration,
		BaseRetryDelay:  5 * time.Second,
		MaxRetryDelay:   15 * time.Minute,
		MaxRetries:      maxRetries,
		BaseURL:         strings.TrimRight(baseURL, "/"),
		From:            from,
		SupportedEvents: []string{EventTypeRegistrationVerification, EventTypePasswordReset},
	}
}

// Validate verifies that the worker configuration has valid parameters and URLs.
func (c WorkerConfig) Validate() error {
	if c.PollInterval <= 0 {
		return errors.New("worker: poll interval must be positive")
	}
	if c.BatchSize <= 0 {
		return errors.New("worker: batch size must be positive")
	}
	if c.LeaseDuration <= 0 {
		return errors.New("worker: lease duration must be positive")
	}
	if c.MaxRetries <= 0 {
		return errors.New("worker: max retries must be positive")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("worker: base URL cannot be empty")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("worker: invalid base URL %q: %v", c.BaseURL, err)
	}
	if len(c.SupportedEvents) == 0 {
		return errors.New("worker: supported event types cannot be empty")
	}
	return nil
}

// CalculateBackoff computes exponential backoff with jitter:
// backoff = 2^(attempts-1) * baseDelay +/- 20% jitter, capped at maxDelay.
func CalculateBackoff(attempts int, baseDelay, maxDelay time.Duration) time.Duration {
	if attempts <= 0 {
		attempts = 1
	}

	shift := attempts - 1
	if shift > 30 {
		shift = 30
	}

	mult := int64(1) << shift
	backoff := time.Duration(mult) * baseDelay
	if backoff > maxDelay || backoff <= 0 {
		backoff = maxDelay
	}

	// Jitter: +/- 20%
	var b [8]byte
	_, _ = rand.Read(b[:])
	randVal := float64(binary.LittleEndian.Uint64(b[:])) / float64(^uint64(0)) // [0.0, 1.0)
	jitterFactor := 0.8 + (randVal * 0.4)                                      // [0.8, 1.2)

	total := time.Duration(float64(backoff) * jitterFactor)
	if total < baseDelay {
		total = baseDelay
	}
	if total > maxDelay {
		total = maxDelay
	}
	return total
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

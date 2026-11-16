package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrInvalidPolicy is returned when a policy has invalid parameters.
	ErrInvalidPolicy = errors.New("ratelimit: invalid policy")

	// ErrEmptyKey is returned when a rate limit key is empty.
	ErrEmptyKey = errors.New("ratelimit: key cannot be empty")

	// ErrInvalidKey is returned when a rate limit key is malformed or exceeds max length.
	ErrInvalidKey = errors.New("ratelimit: invalid key")

	// ErrInvalidSecret is returned when the HMAC secret is insufficient or empty.
	ErrInvalidSecret = errors.New("ratelimit: invalid HMAC secret")
)

const (
	// MaxKeyLength is the maximum allowed length for a rate-limit bucket key.
	MaxKeyLength = 255
)

// Policy defines the token capacity and refill parameters for a token bucket.
//
// Invariant: A policy ID must always map to the same capacity and refill configuration.
// Different policies must never share bucket state.
type Policy struct {
	ID             string        // Unique identifier for the policy (e.g., "auth:login:ip")
	Capacity       int64         // Maximum burst capacity (must be > 0)
	RefillTokens   int64         // Number of tokens replenished per refill interval (must be > 0)
	RefillInterval time.Duration // Duration required to replenish RefillTokens (must be >= 1 microsecond)
}

// Validate validates that policy parameters meet all operational requirements.
func (p Policy) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: policy ID cannot be empty", ErrInvalidPolicy)
	}
	if p.Capacity <= 0 {
		return fmt.Errorf("%w: capacity must be greater than 0, got %d", ErrInvalidPolicy, p.Capacity)
	}
	if p.RefillTokens <= 0 {
		return fmt.Errorf("%w: refill tokens must be greater than 0, got %d", ErrInvalidPolicy, p.RefillTokens)
	}
	if p.RefillInterval < time.Microsecond {
		return fmt.Errorf("%w: refill interval must be at least 1 microsecond, got %v", ErrInvalidPolicy, p.RefillInterval)
	}
	// Prevent integer overflow when converting to microseconds
	if p.RefillInterval.Microseconds() <= 0 {
		return fmt.Errorf("%w: refill interval overflow", ErrInvalidPolicy)
	}
	return nil
}

// Result communicates the decision of an Allow operation.
type Result struct {
	Allowed    bool          // True if the request is permitted and 1 token was consumed
	Remaining  int64         // Number of available tokens remaining in the bucket
	RetryAfter time.Duration // Duration to wait until at least 1 token becomes available (0 if Allowed)
}

// Limiter evaluates rate limits atomically against a persistence backend.
type Limiter interface {
	// Allow evaluates the bucket for the given key against the policy,
	// consuming 1 token if available.
	Allow(ctx context.Context, key string, policy Policy) (Result, error)
}

// Cleaner defines maintenance operations for removing expired or inactive buckets.
type Cleaner interface {
	// Cleanup removes buckets that have not been accessed within the specified retention duration.
	// Returns the number of deleted buckets.
	Cleanup(ctx context.Context, retention time.Duration, limit int) (int64, error)
}

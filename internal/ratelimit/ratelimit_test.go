package ratelimit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/ratelimit"
)

func TestPolicy_Validate(t *testing.T) {
	tests := []struct {
		name      string
		policy    ratelimit.Policy
		expectErr bool
	}{
		{
			name: "valid standard policy",
			policy: ratelimit.Policy{
				ID:             "test:policy",
				Capacity:       10,
				RefillTokens:   1,
				RefillInterval: 2 * time.Second,
			},
			expectErr: false,
		},
		{
			name: "empty policy ID",
			policy: ratelimit.Policy{
				ID:             "",
				Capacity:       10,
				RefillTokens:   1,
				RefillInterval: time.Second,
			},
			expectErr: true,
		},
		{
			name: "whitespace policy ID",
			policy: ratelimit.Policy{
				ID:             "   ",
				Capacity:       10,
				RefillTokens:   1,
				RefillInterval: time.Second,
			},
			expectErr: true,
		},
		{
			name: "zero capacity",
			policy: ratelimit.Policy{
				ID:             "test",
				Capacity:       0,
				RefillTokens:   1,
				RefillInterval: time.Second,
			},
			expectErr: true,
		},
		{
			name: "negative capacity",
			policy: ratelimit.Policy{
				ID:             "test",
				Capacity:       -5,
				RefillTokens:   1,
				RefillInterval: time.Second,
			},
			expectErr: true,
		},
		{
			name: "zero refill tokens",
			policy: ratelimit.Policy{
				ID:             "test",
				Capacity:       10,
				RefillTokens:   0,
				RefillInterval: time.Second,
			},
			expectErr: true,
		},
		{
			name: "negative refill tokens",
			policy: ratelimit.Policy{
				ID:             "test",
				Capacity:       10,
				RefillTokens:   -1,
				RefillInterval: time.Second,
			},
			expectErr: true,
		},
		{
			name: "zero refill interval",
			policy: ratelimit.Policy{
				ID:             "test",
				Capacity:       10,
				RefillTokens:   1,
				RefillInterval: 0,
			},
			expectErr: true,
		},
		{
			name: "sub-microsecond interval",
			policy: ratelimit.Policy{
				ID:             "test",
				Capacity:       10,
				RefillTokens:   1,
				RefillInterval: 500 * time.Nanosecond,
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() err = %v, expectErr = %v", err, tt.expectErr)
			}
			if tt.expectErr && !errors.Is(err, ratelimit.ErrInvalidPolicy) {
				t.Errorf("expected ErrInvalidPolicy, got %v", err)
			}
		})
	}
}

func TestRefillArithmetic(t *testing.T) {
	// Invariant test: verify integer token refill and retry calculations
	policy := ratelimit.Policy{
		ID:             "calc:test",
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: 2 * time.Second, // 2,000,000 micros
	}

	intervalMicros := policy.RefillInterval.Microseconds()

	// 1. Halfway through interval: tokensToAdd must be 0 (no fractional token, no drift)
	elapsed1 := 1 * time.Second
	tokensToAdd1 := (elapsed1.Microseconds() * policy.RefillTokens) / intervalMicros
	if tokensToAdd1 != 0 {
		t.Errorf("expected 0 tokens at 1s, got %d", tokensToAdd1)
	}

	// 2. Exactly 1 interval: tokensToAdd must be 1
	elapsed2 := 2 * time.Second
	tokensToAdd2 := (elapsed2.Microseconds() * policy.RefillTokens) / intervalMicros
	if tokensToAdd2 != 1 {
		t.Errorf("expected 1 token at 2s, got %d", tokensToAdd2)
	}

	// 3. Multi-interval with remainder: 5.5s with 2s interval => 2 tokens, remainder 1.5s
	elapsed3 := 5500 * time.Millisecond
	tokensToAdd3 := (elapsed3.Microseconds() * policy.RefillTokens) / intervalMicros
	if tokensToAdd3 != 2 {
		t.Errorf("expected 2 tokens at 5.5s, got %d", tokensToAdd3)
	}

	timeUsedMicros := (tokensToAdd3 * intervalMicros) / policy.RefillTokens
	if timeUsedMicros != 4000000 { // 4 seconds consumed
		t.Errorf("expected 4,000,000 micros consumed, got %d", timeUsedMicros)
	}

	// 4. RetryAfter calculation for 1 token:
	// If 0.5s has elapsed, remaining wait must be 1.5s
	elapsed4 := 500 * time.Millisecond
	timeForOneToken := (intervalMicros + policy.RefillTokens - 1) / policy.RefillTokens
	retryMicros := timeForOneToken - elapsed4.Microseconds()
	retryDuration := time.Duration(retryMicros) * time.Microsecond
	if retryDuration != 1500*time.Millisecond {
		t.Errorf("expected 1.5s retry duration, got %v", retryDuration)
	}
}

package ratelimit

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// DefaultRetention is the default retention period for inactive rate limit buckets.
	DefaultRetention = 24 * time.Hour

	// DefaultCleanupBatchSize is the default maximum number of buckets to clean in one pass.
	DefaultCleanupBatchSize = 500
)

// Config holds the configuration for rate limiting infrastructure.
type Config struct {
	HMACSecret       []byte
	DefaultRetention time.Duration
	CleanupBatchSize int
}

// ConfigFromEnv reads rate limiting configuration from environment variables.
// The HMAC secret is read from RATE_LIMIT_HMAC_SECRET (as 64-character hex, base64, or raw string of >= 32 bytes).
func ConfigFromEnv() (Config, error) {
	rawSecret := strings.TrimSpace(os.Getenv("RATE_LIMIT_HMAC_SECRET"))
	if rawSecret == "" {
		return Config{}, fmt.Errorf("%w: RATE_LIMIT_HMAC_SECRET is required", ErrInvalidSecret)
	}

	secretBytes, err := ParseSecret(rawSecret)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidSecret, err)
	}

	return Config{
		HMACSecret:       secretBytes,
		DefaultRetention: DefaultRetention,
		CleanupBatchSize: DefaultCleanupBatchSize,
	}, nil
}

// ParseSecret parses an HMAC secret from hex, base64, or raw bytes.
// The secret must be at least 32 bytes (256 bits).
func ParseSecret(encoded string) ([]byte, error) {
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return nil, errors.New("secret cannot be empty")
	}

	// 1. Try 64-character hexadecimal
	if len(trimmed) == 64 {
		if b, err := hex.DecodeString(trimmed); err == nil && len(b) >= MinHMACSecretBytes {
			return b, nil
		}
	}

	// 2. Try standard base64 or raw URL base64
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(b) >= MinHMACSecretBytes {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil && len(b) >= MinHMACSecretBytes {
		return b, nil
	}

	// 3. Fallback: raw string bytes if length >= 32
	if len(trimmed) >= MinHMACSecretBytes {
		return []byte(trimmed), nil
	}

	return nil, fmt.Errorf("secret must be at least %d bytes (got %d)", MinHMACSecretBytes, len(trimmed))
}

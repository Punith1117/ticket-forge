package ratelimit_test

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/ratelimit"
)

func TestParseSecret(t *testing.T) {
	raw32 := "01234567890123456789012345678901" // exactly 32 bytes
	hex64 := hex.EncodeToString([]byte(raw32))
	b64 := base64.StdEncoding.EncodeToString([]byte(raw32))

	tests := []struct {
		name      string
		input     string
		expectErr bool
	}{
		{"valid 64 hex chars", hex64, false},
		{"valid base64", b64, false},
		{"valid raw 32-byte string", raw32, false},
		{"empty string", "", true},
		{"too short string", "short-secret", true},
		{"too short hex string", hex.EncodeToString([]byte("short")), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := ratelimit.ParseSecret(tt.input)
			if (err != nil) != tt.expectErr {
				t.Fatalf("expected err=%v, got %v", tt.expectErr, err)
			}
			if !tt.expectErr && len(b) < 32 {
				t.Errorf("expected at least 32 bytes, got %d", len(b))
			}
		})
	}
}

func TestConfigFromEnv(t *testing.T) {
	orig := os.Getenv("RATE_LIMIT_HMAC_SECRET")
	defer os.Setenv("RATE_LIMIT_HMAC_SECRET", orig)

	t.Run("missing secret returns error", func(t *testing.T) {
		os.Unsetenv("RATE_LIMIT_HMAC_SECRET")
		_, err := ratelimit.ConfigFromEnv()
		if err == nil {
			t.Fatal("expected error when RATE_LIMIT_HMAC_SECRET is missing, got nil")
		}
	})

	t.Run("valid secret loads config successfully", func(t *testing.T) {
		secretHex := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes
		os.Setenv("RATE_LIMIT_HMAC_SECRET", secretHex)

		cfg, err := ratelimit.ConfigFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.HMACSecret) != 32 {
			t.Errorf("expected 32 secret bytes, got %d", len(cfg.HMACSecret))
		}
		if cfg.DefaultRetention <= 0 {
			t.Errorf("expected positive retention, got %v", cfg.DefaultRetention)
		}
		if cfg.CleanupBatchSize <= 0 {
			t.Errorf("expected positive batch size, got %d", cfg.CleanupBatchSize)
		}
	})
}

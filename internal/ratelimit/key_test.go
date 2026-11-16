package ratelimit_test

import (
	"crypto/rand"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/ratelimit"
)

func generateTestSecret(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to generate random secret: %v", err)
	}
	return b
}

func TestNewKeyDeriver_Validation(t *testing.T) {
	t.Run("rejects secret smaller than 32 bytes", func(t *testing.T) {
		shortSecret := []byte("short-secret-under-32-bytes")
		_, err := ratelimit.NewKeyDeriver(shortSecret)
		if err == nil {
			t.Fatal("expected error for short secret, got nil")
		}
	})

	t.Run("accepts 32-byte secret", func(t *testing.T) {
		secret := generateTestSecret(t)
		d, err := ratelimit.NewKeyDeriver(secret)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d == nil {
			t.Fatal("expected non-nil KeyDeriver")
		}
	})
}

func TestKeyDeriver_DeriveKey_Determinism(t *testing.T) {
	secret := generateTestSecret(t)
	d, err := ratelimit.NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("failed to create deriver: %v", err)
	}

	key1, err := d.DeriveKey("auth:login:ip", ratelimit.DomainIP, "192.168.1.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	key2, err := d.DeriveKey("auth:login:ip", ratelimit.DomainIP, "192.168.1.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if key1 != key2 {
		t.Errorf("expected deterministic keys, got %q and %q", key1, key2)
	}

	// Verify key format: starts with policy ID prefix
	if !strings.HasPrefix(key1, "auth:login:ip:") {
		t.Errorf("expected key to start with policy prefix, got %q", key1)
	}

	// Verify raw identifier is NOT present in key
	if strings.Contains(key1, "192.168.1.1") {
		t.Errorf("raw identifier must not be exposed in key: %q", key1)
	}
}

func TestKeyDeriver_DomainSeparation(t *testing.T) {
	secret := generateTestSecret(t)
	d, err := ratelimit.NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("failed to create deriver: %v", err)
	}

	rawID := "test@example.com"

	// 1. Same raw identifier with different domains must produce different keys
	emailKey, err := d.DeriveKey("test:policy", ratelimit.DomainEmail, rawID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokenKey, err := d.DeriveKey("test:policy", ratelimit.DomainToken, rawID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if emailKey == tokenKey {
		t.Errorf("keys across different domains must not collide: %q == %q", emailKey, tokenKey)
	}

	// 2. Same identifier and domain with different policies must produce different keys
	keyPolicyA, err := d.DeriveKey("policy:a", ratelimit.DomainEmail, rawID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	keyPolicyB, err := d.DeriveKey("policy:b", ratelimit.DomainEmail, rawID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if keyPolicyA == keyPolicyB {
		t.Errorf("keys across different policies must not collide: %q == %q", keyPolicyA, keyPolicyB)
	}
}

func TestKeyDeriver_InputValidation(t *testing.T) {
	secret := generateTestSecret(t)
	d, err := ratelimit.NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("failed to create deriver: %v", err)
	}

	tests := []struct {
		name      string
		policy    string
		domain    ratelimit.Domain
		rawID     string
		expectErr bool
	}{
		{"empty policy", "", ratelimit.DomainIP, "1.2.3.4", true},
		{"empty domain", "policy", "", "1.2.3.4", true},
		{"empty identifier", "policy", ratelimit.DomainIP, "", true},
		{"valid inputs", "policy", ratelimit.DomainIP, "1.2.3.4", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.DeriveKey(tt.policy, tt.domain, tt.rawID)
			if (err != nil) != tt.expectErr {
				t.Errorf("expected err=%v, got %v", tt.expectErr, err)
			}
		})
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		expectErr bool
	}{
		{"empty key", "", true},
		{"whitespace key", "   ", true},
		{"no colon separator", "invalidkeywithoutcolon", true},
		{"key exceeding max length", strings.Repeat("a", 256), true},
		{"valid key", "policy:1234567890abcdef", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ratelimit.ValidateKey(tt.key)
			if (err != nil) != tt.expectErr {
				t.Errorf("expected err=%v, got %v", tt.expectErr, err)
			}
		})
	}
}

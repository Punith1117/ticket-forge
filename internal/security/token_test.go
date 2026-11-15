package security_test

import (
	"encoding/base64"
	"errors"
	"regexp"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/security"
)

func TestGenerateToken_SuccessAndEntropy(t *testing.T) {
	token, err := security.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if len(token) != 43 {
		t.Errorf("expected 43 base64url characters for 32 bytes entropy, got %d (token: %q)", len(token), token)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token is not valid unpadded base64url: %v", err)
	}

	if len(decoded) != security.DefaultTokenEntropyBytes {
		t.Errorf("expected %d decoded bytes, got %d", security.DefaultTokenEntropyBytes, len(decoded))
	}

	urlSafePattern := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !urlSafePattern.MatchString(token) {
		t.Errorf("token contains non-url-safe characters: %q", token)
	}
}

func TestGenerateToken_Uniqueness(t *testing.T) {
	const iterations = 1000
	seen := make(map[string]struct{}, iterations)

	for i := 0; i < iterations; i++ {
		tok, err := security.GenerateToken()
		if err != nil {
			t.Fatalf("iteration %d: GenerateToken failed: %v", i, err)
		}
		if _, exists := seen[tok]; exists {
			t.Fatalf("collision detected on iteration %d: %s", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestGenerateTokenWithEntropy_Validation(t *testing.T) {
	// Rejection under 32 bytes (256 bits)
	_, err := security.GenerateTokenWithEntropy(31)
	if err == nil {
		t.Fatal("expected error for entropy < 32 bytes, got nil")
	}
	if !errors.Is(err, security.ErrInsufficientEntropy) {
		t.Errorf("expected ErrInsufficientEntropy, got: %v", err)
	}

	_, err = security.GenerateTokenWithEntropy(0)
	if err == nil {
		t.Fatal("expected error for 0 bytes entropy, got nil")
	}

	// Success with custom high entropy (64 bytes = 512 bits)
	tok, err := security.GenerateTokenWithEntropy(64)
	if err != nil {
		t.Fatalf("GenerateTokenWithEntropy(64) failed: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("failed to decode 64-byte token: %v", err)
	}
	if len(decoded) != 64 {
		t.Errorf("expected 64 decoded bytes, got %d", len(decoded))
	}
}

func TestHashToken_FormatAndVectors(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			input:    "abc",
			expected: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		},
		{
			input:    "TicketForgeSecureToken12345",
			expected: "87ee63931e3a8ee5c58c5d34606ae38607c67489f8474ffffd8aa6019c1bf178",
		},
	}

	hex64Pattern := regexp.MustCompile(`^[0-9a-f]{64}$`)

	for _, tt := range tests {
		t.Run("hash_"+tt.input, func(t *testing.T) {
			actual := security.HashToken(tt.input)
			if actual != tt.expected {
				t.Errorf("expected hash %s, got %s", tt.expected, actual)
			}
			if !hex64Pattern.MatchString(actual) {
				t.Errorf("hash does not match ^[0-9a-f]{64}$: %s", actual)
			}
		})
	}

	// Distinct inputs yield distinct hashes
	h1 := security.HashToken("token_alpha")
	h2 := security.HashToken("token_beta")
	if h1 == h2 {
		t.Errorf("expected different hashes, but got identical: %s", h1)
	}
}

func TestCompareTokenHashes(t *testing.T) {
	h1 := security.HashToken("sample_token")
	h2 := security.HashToken("sample_token")
	h3 := security.HashToken("other_token")

	if !security.CompareTokenHashes(h1, h2) {
		t.Error("expected CompareTokenHashes to return true for identical hashes")
	}
	if security.CompareTokenHashes(h1, h3) {
		t.Error("expected CompareTokenHashes to return false for different hashes")
	}
	if security.CompareTokenHashes(h1, "short") {
		t.Error("expected CompareTokenHashes to return false for different length")
	}
}

func TestValidateTokenHash(t *testing.T) {
	raw := "secret_verification_token_sample"
	expectedHash := security.HashToken(raw)

	if !security.ValidateTokenHash(raw, expectedHash) {
		t.Error("expected ValidateTokenHash to succeed for correct token and hash")
	}

	if security.ValidateTokenHash(raw+"_tampered", expectedHash) {
		t.Error("expected ValidateTokenHash to fail for incorrect token")
	}

	if security.ValidateTokenHash(raw, "0000000000000000000000000000000000000000000000000000000000000000") {
		t.Error("expected ValidateTokenHash to fail for incorrect expected hash")
	}
}

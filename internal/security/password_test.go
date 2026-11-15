package security_test

import (
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/security"
)

func TestPassword_HashAndVerify_Success(t *testing.T) {
	password := "correct-horse-battery-staple-1234"

	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("unexpected hash format prefix: %s", hash)
	}

	match, err := security.VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword failed: %v", err)
	}
	if !match {
		t.Error("expected password to match hash")
	}
}

func TestPassword_Verify_WrongPassword(t *testing.T) {
	password := "correct-horse-battery-staple-1234"
	wrongPassword := "wrong-horse-battery-staple-1234"

	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	match, err := security.VerifyPassword(wrongPassword, hash)
	if err != nil {
		t.Fatalf("VerifyPassword failed: %v", err)
	}
	if match {
		t.Error("expected wrong password NOT to match hash")
	}
}

func TestPassword_UniqueSalts(t *testing.T) {
	password := "same-password-different-hashes-1234"

	hash1, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword 1 failed: %v", err)
	}
	hash2, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword 2 failed: %v", err)
	}

	if hash1 == hash2 {
		t.Error("expected different hashes for repeated hashing of same password due to random salting")
	}
}

func TestPassword_StrengthValidation(t *testing.T) {
	testCases := []struct {
		name     string
		password string
		valid    bool
	}{
		{"too short (11 chars)", "short123456", false},
		{"empty password", "", false},
		{"minimum valid length (12 chars)", "123456789012", true},
		{"long valid password (64 chars)", strings.Repeat("a", 64), true},
		{"max valid length (128 chars)", strings.Repeat("b", 128), true},
		{"too long (129 chars)", strings.Repeat("c", 129), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := security.ValidatePasswordStrength(tc.password)
			if tc.valid && err != nil {
				t.Errorf("expected password to be valid, got error: %v", err)
			}
			if !tc.valid && err == nil {
				t.Errorf("expected password to be rejected, got nil")
			}
		})
	}
}

func TestPassword_Verify_MalformedHashes(t *testing.T) {
	testCases := []struct {
		name string
		hash string
	}{
		{"empty string", ""},
		{"not enough parts", "$argon2id$v=19$m=65536,t=3,p=2"},
		{"unsupported algorithm", "$bcrypt$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"invalid version", "$argon2id$v=99$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"corrupted base64 salt", "$argon2id$v=19$m=65536,t=3,p=2$!!!invalid-b64$aGFzaA"},
		{"corrupted base64 hash", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$!!!invalid-b64"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			match, err := security.VerifyPassword("password-123456", tc.hash)
			if err == nil {
				t.Errorf("expected error for malformed hash %q, got nil", tc.hash)
			}
			if match {
				t.Error("match should be false for invalid hash")
			}
		})
	}
}

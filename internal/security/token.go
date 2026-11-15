package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	// DefaultTokenEntropyBytes is the default entropy size (32 bytes = 256 bits)
	// required for high-entropy tokens.
	DefaultTokenEntropyBytes = 32

	// MinTokenEntropyBytes is the absolute minimum entropy size allowed (256 bits).
	MinTokenEntropyBytes = 32
)

var (
	// ErrInsufficientEntropy indicates that the requested token entropy is below 256 bits.
	ErrInsufficientEntropy = errors.New("security: minimum token entropy is 32 bytes (256 bits)")

	// ErrInvalidToken indicates that a provided token is empty or invalid.
	ErrInvalidToken = errors.New("security: invalid token")
)

// GenerateToken generates a cryptographically secure, unpadded base64url-encoded token
// with 256 bits (32 bytes) of random entropy.
func GenerateToken() (string, error) {
	return GenerateTokenWithEntropy(DefaultTokenEntropyBytes)
}

// GenerateTokenWithEntropy generates a cryptographically secure, unpadded base64url-encoded token
// with the specified number of entropy bytes (minimum 32 bytes).
func GenerateTokenWithEntropy(entropyBytes int) (string, error) {
	if entropyBytes < MinTokenEntropyBytes {
		return "", fmt.Errorf("%w: requested %d bytes", ErrInsufficientEntropy, entropyBytes)
	}

	b := make([]byte, entropyBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("security: failed to read secure random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken computes the SHA-256 digest of a raw token and returns it
// as a 64-character lowercase hexadecimal string, matching the database constraint ^[0-9a-f]{64}$.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CompareTokenHashes performs a constant-time comparison of two token hash strings
// to prevent timing attacks.
func CompareTokenHashes(hashA, hashB string) bool {
	return subtle.ConstantTimeCompare([]byte(hashA), []byte(hashB)) == 1
}

// ValidateTokenHash verifies whether the SHA-256 hash of a raw token matches an expected hash
// using constant-time comparison.
func ValidateTokenHash(rawToken, expectedHash string) bool {
	actualHash := HashToken(rawToken)
	return CompareTokenHashes(actualHash, expectedHash)
}

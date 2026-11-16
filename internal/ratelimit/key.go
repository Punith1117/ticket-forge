package ratelimit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// MinHMACSecretBytes is the minimum length for the HMAC secret (256 bits / 32 bytes).
	MinHMACSecretBytes = 32

	// AppNamespace is bound into the HMAC domain separation to isolate TicketForge rate limits.
	AppNamespace = "ticketforge"

	// KeyDerivationVersion is the schema version for rate-limiting key derivation.
	KeyDerivationVersion = "v1"
)

// Domain represents the category of the identifier being hashed.
type Domain string

const (
	// DomainIP identifies client IP addresses.
	DomainIP Domain = "ip"

	// DomainEmail identifies user/challenge normalized email addresses.
	DomainEmail Domain = "email"

	// DomainToken identifies registration or authentication token identifiers.
	DomainToken Domain = "token"
)

// KeyDeriver derives deterministic, privacy-preserving rate-limit bucket keys using keyed HMAC-SHA256.
//
// Invariant: Buckets are uniquely identified by both policy ID and HMAC-derived identifier.
// Domain separation binds application namespace, version, policy ID, identifier domain, and raw identifier.
type KeyDeriver struct {
	secret []byte
}

// NewKeyDeriver constructs a KeyDeriver with the provided secret.
func NewKeyDeriver(secret []byte) (*KeyDeriver, error) {
	if len(secret) < MinHMACSecretBytes {
		return nil, fmt.Errorf("%w: secret must be at least %d bytes, got %d", ErrInvalidSecret, MinHMACSecretBytes, len(secret))
	}
	secCopy := make([]byte, len(secret))
	copy(secCopy, secret)
	return &KeyDeriver{secret: secCopy}, nil
}

// DeriveKey derives a unique, domain-separated rate limit bucket key.
// The key format is `<policyID>:<hex(HMAC-SHA256)>`.
func (d *KeyDeriver) DeriveKey(policyID string, domain Domain, rawIdentifier string) (string, error) {
	trimmedPolicy := strings.TrimSpace(policyID)
	if trimmedPolicy == "" {
		return "", fmt.Errorf("%w: policy ID cannot be empty", ErrInvalidPolicy)
	}
	trimmedDomain := strings.TrimSpace(string(domain))
	if trimmedDomain == "" {
		return "", fmt.Errorf("%w: domain cannot be empty", ErrInvalidKey)
	}
	trimmedID := strings.TrimSpace(rawIdentifier)
	if trimmedID == "" {
		return "", fmt.Errorf("%w: raw identifier cannot be empty", ErrInvalidKey)
	}

	h := hmac.New(sha256.New, d.secret)
	// Bind application namespace, version, policy ID, domain, and raw identifier
	_, _ = fmt.Fprintf(h, "%s:%s:%s:%s:%s", AppNamespace, KeyDerivationVersion, trimmedPolicy, trimmedDomain, trimmedID)
	mac := h.Sum(nil)

	key := fmt.Sprintf("%s:%s", trimmedPolicy, hex.EncodeToString(mac))
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// ValidateKey checks that a bucket key is non-empty, within size constraints, and formatted properly.
func ValidateKey(key string) error {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ErrEmptyKey
	}
	if len(trimmed) > MaxKeyLength {
		return fmt.Errorf("%w: key length %d exceeds maximum %d", ErrInvalidKey, len(trimmed), MaxKeyLength)
	}
	if !strings.Contains(trimmed, ":") {
		return fmt.Errorf("%w: key must contain a policy separator ':'", ErrInvalidKey)
	}
	return nil
}

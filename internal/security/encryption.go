package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// AES256KeySize is the required key length for AES-256 (32 bytes = 256 bits).
	AES256KeySize = 32

	// GCMNonceSize is the standard 96-bit (12-byte) nonce size for AES-GCM.
	GCMNonceSize = 12

	// PurposeRegistrationVerification is the standard AAD domain separator for registration tokens.
	PurposeRegistrationVerification = "auth:registration_verification"

	// PurposePasswordReset is the standard AAD domain separator for password-reset tokens.
	PurposePasswordReset = "auth:password_reset"
)

var (
	// ErrInvalidKey indicates that an encryption key is invalid or has wrong dimensions.
	ErrInvalidKey = errors.New("security: invalid encryption key")

	// ErrUnknownKeyVersion indicates that a ciphertext references an unknown key version.
	ErrUnknownKeyVersion = errors.New("security: unknown encryption key version")

	// ErrDecryptionFailed indicates that decryption or authentication tag verification failed.
	ErrDecryptionFailed = errors.New("security: decryption failed (corrupted ciphertext, mismatched AAD, or wrong key)")
)

// EncryptedToken represents an authenticated AES-GCM encrypted token payload
// suitable for JSON serialization in transactional outbox events.
type EncryptedToken struct {
	KeyVersion int    `json:"key_version"`
	Nonce      string `json:"nonce"`      // Standard base64-encoded 12-byte nonce
	Ciphertext string `json:"ciphertext"` // Standard base64-encoded ciphertext + 16-byte GCM auth tag
}

// String implements fmt.Stringer to ensure encrypted token material is not accidentally dumped in logs.
func (e EncryptedToken) String() string {
	return fmt.Sprintf("EncryptedToken(v%d)", e.KeyVersion)
}

// Validate checks whether the EncryptedToken has non-empty fields and a valid key version.
func (e EncryptedToken) Validate() error {
	if e.KeyVersion <= 0 {
		return fmt.Errorf("%w: key_version must be positive, got %d", ErrInvalidToken, e.KeyVersion)
	}
	if strings.TrimSpace(e.Nonce) == "" {
		return fmt.Errorf("%w: nonce cannot be empty", ErrInvalidToken)
	}
	if strings.TrimSpace(e.Ciphertext) == "" {
		return fmt.Errorf("%w: ciphertext cannot be empty", ErrInvalidToken)
	}
	return nil
}

// KeyRing holds encryption keys indexed by version, distinguishing the active
// encryption key from historical decryption keys to support zero-downtime key rotation.
type KeyRing struct {
	activeVersion int
	keys          map[int][]byte
}

// NewKeyRing constructs and validates a KeyRing.
func NewKeyRing(activeVersion int, keys map[int][]byte) (*KeyRing, error) {
	if activeVersion <= 0 {
		return nil, fmt.Errorf("%w: activeVersion must be > 0, got %d", ErrInvalidKey, activeVersion)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: at least one key must be provided", ErrInvalidKey)
	}

	copiedKeys := make(map[int][]byte, len(keys))
	for ver, key := range keys {
		if ver <= 0 {
			return nil, fmt.Errorf("%w: key version must be > 0, got %d", ErrInvalidKey, ver)
		}
		if len(key) != AES256KeySize {
			return nil, fmt.Errorf("%w: key for version %d must be exactly %d bytes, got %d", ErrInvalidKey, ver, AES256KeySize, len(key))
		}
		// Defensive copy
		k := make([]byte, len(key))
		copy(k, key)
		copiedKeys[ver] = k
	}

	if _, ok := copiedKeys[activeVersion]; !ok {
		return nil, fmt.Errorf("%w: active key version %d not found in provided keys", ErrInvalidKey, activeVersion)
	}

	return &KeyRing{
		activeVersion: activeVersion,
		keys:          copiedKeys,
	}, nil
}

// ActiveVersion returns the active encryption key version.
func (r *KeyRing) ActiveVersion() int {
	return r.activeVersion
}

// Key retrieves the raw key bytes for a given version.
func (r *KeyRing) Key(version int) ([]byte, bool) {
	k, ok := r.keys[version]
	if !ok {
		return nil, false
	}
	// Return defensive copy
	cp := make([]byte, len(k))
	copy(cp, k)
	return cp, true
}

// TokenEncrypter provides authenticated encryption and decryption for raw token strings.
type TokenEncrypter struct {
	keyRing *KeyRing
}

// NewTokenEncrypter constructs a TokenEncrypter with the given KeyRing.
func NewTokenEncrypter(keyRing *KeyRing) (*TokenEncrypter, error) {
	if keyRing == nil {
		return nil, fmt.Errorf("%w: key ring cannot be nil", ErrInvalidKey)
	}
	return &TokenEncrypter{keyRing: keyRing}, nil
}

// ActiveKeyVersion returns the currently active key version used for encryption.
func (e *TokenEncrypter) ActiveKeyVersion() int {
	return e.keyRing.ActiveVersion()
}

// EncryptToken encrypts a raw token string using AES-256-GCM with a fresh unique nonce,
// binding the ciphertext to the provided Authenticated Additional Data (AAD).
func (e *TokenEncrypter) EncryptToken(rawToken string, aad []byte) (EncryptedToken, error) {
	if strings.TrimSpace(rawToken) == "" {
		return EncryptedToken{}, fmt.Errorf("%w: raw token cannot be empty", ErrInvalidToken)
	}

	activeVer := e.keyRing.ActiveVersion()
	key, ok := e.keyRing.Key(activeVer)
	if !ok {
		return EncryptedToken{}, fmt.Errorf("%w: active key version %d missing from ring", ErrInvalidKey, activeVer)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return EncryptedToken{}, fmt.Errorf("security: failed to create cipher block: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedToken{}, fmt.Errorf("security: failed to create GCM AEAD: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return EncryptedToken{}, fmt.Errorf("security: failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(rawToken), aad)

	return EncryptedToken{
		KeyVersion: activeVer,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

// DecryptToken decrypts an EncryptedToken using the historical key specified by its KeyVersion,
// verifying authentication and AAD match.
func (e *TokenEncrypter) DecryptToken(enc EncryptedToken, aad []byte) (string, error) {
	if err := enc.Validate(); err != nil {
		return "", err
	}

	key, ok := e.keyRing.Key(enc.KeyVersion)
	if !ok {
		return "", fmt.Errorf("%w: version %d", ErrUnknownKeyVersion, enc.KeyVersion)
	}

	nonce, err := base64.StdEncoding.DecodeString(enc.Nonce)
	if err != nil {
		return "", fmt.Errorf("%w: invalid nonce encoding: %v", ErrDecryptionFailed, err)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(enc.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: invalid ciphertext encoding: %v", ErrDecryptionFailed, err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("security: failed to create cipher block: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("security: failed to create GCM AEAD: %w", err)
	}

	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("%w: invalid nonce length %d, expected %d", ErrDecryptionFailed, len(nonce), gcm.NonceSize())
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	return string(plaintext), nil
}

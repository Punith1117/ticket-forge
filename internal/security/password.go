package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// Argon2id algorithm identifier
	Argon2idAlgorithm = "argon2id"

	// Current Argon2 version
	Argon2Version = argon2.Version

	// Default Argon2id parameters recommended for interactive logins
	DefaultArgon2Time    uint32 = 3
	DefaultArgon2Memory  uint32 = 64 * 1024 // 64 MB
	DefaultArgon2Threads uint8  = 2
	DefaultArgon2KeyLen  uint32 = 32
	DefaultArgon2SaltLen uint32 = 16

	// Password length constraints
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

var (
	// ErrInvalidPassword indicates a password that does not satisfy complexity/length rules.
	ErrInvalidPassword = errors.New("security: password must be between 12 and 128 characters")

	// ErrInvalidHash indicates a malformed or incompatible encoded password hash.
	ErrInvalidHash = errors.New("security: encoded password hash is malformed")

	// ErrIncompatibleAlgorithm indicates a password hash that uses an unsupported algorithm or version.
	ErrIncompatibleAlgorithm = errors.New("security: incompatible password hash algorithm or version")
)

// PasswordParams holds the parameters used for Argon2id hashing.
type PasswordParams struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultPasswordParams returns the production default parameters for Argon2id.
func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		Time:    DefaultArgon2Time,
		Memory:  DefaultArgon2Memory,
		Threads: DefaultArgon2Threads,
		KeyLen:  DefaultArgon2KeyLen,
		SaltLen: DefaultArgon2SaltLen,
	}
}

// ValidatePasswordStrength checks that a plain-text password meets the length requirements.
func ValidatePasswordStrength(password string) error {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return ErrInvalidPassword
	}
	return nil
}

// HashPassword hashes a plain-text password using Argon2id with default parameters and a fresh random salt.
// Returns a standard PHC-formatted string:
// $argon2id$v=19$m=65536,t=3,p=2$<base64-salt>$<base64-hash>
func HashPassword(password string) (string, error) {
	return HashPasswordWithParams(password, DefaultPasswordParams())
}

// HashPasswordWithParams hashes a password with specific Argon2id parameters.
func HashPasswordWithParams(password string, params PasswordParams) (string, error) {
	if err := ValidatePasswordStrength(password); err != nil {
		return "", err
	}

	salt := make([]byte, params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: failed to generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		Argon2idAlgorithm,
		Argon2Version,
		params.Memory,
		params.Time,
		params.Threads,
		b64Salt,
		b64Hash,
	)

	return encoded, nil
}

// VerifyPassword verifies a plain-text password against a standard PHC-encoded Argon2id hash.
// Uses constant-time comparison to prevent timing side-channel attacks.
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	// Expected parts: ["", "argon2id", "v=19", "m=65536,t=3,p=2", "<salt>", "<hash>"]
	if len(parts) != 6 {
		return false, ErrInvalidHash
	}

	if parts[1] != Argon2idAlgorithm {
		return false, fmt.Errorf("%w: unsupported algorithm %q", ErrIncompatibleAlgorithm, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("%w: invalid version string %q", ErrInvalidHash, parts[2])
	}
	if version != Argon2Version {
		return false, fmt.Errorf("%w: unsupported version %d", ErrIncompatibleAlgorithm, version)
	}

	var params PasswordParams
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Time, &threads); err != nil {
		return false, fmt.Errorf("%w: invalid parameters string %q", ErrInvalidHash, parts[3])
	}
	params.Threads = uint8(threads)

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("%w: invalid salt encoding", ErrInvalidHash)
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("%w: invalid hash encoding", ErrInvalidHash)
	}
	params.KeyLen = uint32(len(expectedHash))

	computedHash := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)

	if subtle.ConstantTimeCompare(expectedHash, computedHash) == 1 {
		return true, nil
	}

	return false, nil
}

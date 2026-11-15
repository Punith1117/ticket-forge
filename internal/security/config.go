package security

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// EncryptionConfig holds the environment configuration for AES-256 token encryption keys.
type EncryptionConfig struct {
	ActiveVersion int
	Keys          map[int]string // version -> encoded key string (hex or base64)
}

// EncryptionConfigFromEnv reads token encryption configuration from environment variables.
//
// Supported environment patterns:
//  1. TOKEN_ENCRYPTION_KEY=<key>: single active key assigned to version 1 (local development).
//  2. TOKEN_ENCRYPTION_ACTIVE_KEY_VERSION=<int>: active key version (default: 1).
//  3. TOKEN_ENCRYPTION_KEYS="1:<key>,2:<key>": multi-version key ring for rotation.
//  4. TOKEN_ENCRYPTION_KEY_V<N>=<key>: version-specific environment keys.
func EncryptionConfigFromEnv() EncryptionConfig {
	cfg := EncryptionConfig{
		ActiveVersion: 1,
		Keys:          make(map[int]string),
	}

	if vStr := os.Getenv("TOKEN_ENCRYPTION_ACTIVE_KEY_VERSION"); vStr != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(vStr)); err == nil && v > 0 {
			cfg.ActiveVersion = v
		}
	}

	// 1. Single default key
	if singleKey := strings.TrimSpace(os.Getenv("TOKEN_ENCRYPTION_KEY")); singleKey != "" {
		cfg.Keys[1] = singleKey
	}

	// 2. Comma-separated version:key pairs, e.g. "1:hexkey1,2:hexkey2"
	if multiKeys := strings.TrimSpace(os.Getenv("TOKEN_ENCRYPTION_KEYS")); multiKeys != "" {
		pairs := strings.Split(multiKeys, ",")
		for _, pair := range pairs {
			parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
			if len(parts) == 2 {
				if ver, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil && ver > 0 {
					cfg.Keys[ver] = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// 3. Environment variables matching TOKEN_ENCRYPTION_KEY_V<N>
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[0], "TOKEN_ENCRYPTION_KEY_V") {
			verStr := strings.TrimPrefix(parts[0], "TOKEN_ENCRYPTION_KEY_V")
			if ver, err := strconv.Atoi(verStr); err == nil && ver > 0 {
				cfg.Keys[ver] = strings.TrimSpace(parts[1])
			}
		}
	}

	return cfg
}

// BuildKeyRing parses, decodes, and validates all keys in the configuration,
// returning a ready-to-use KeyRing.
func (c EncryptionConfig) BuildKeyRing() (*KeyRing, error) {
	if c.ActiveVersion <= 0 {
		return nil, fmt.Errorf("%w: active version must be a positive integer, got %d", ErrInvalidKey, c.ActiveVersion)
	}
	if len(c.Keys) == 0 {
		return nil, fmt.Errorf("%w: no encryption keys configured (set TOKEN_ENCRYPTION_KEY or TOKEN_ENCRYPTION_KEYS)", ErrInvalidKey)
	}

	decodedKeys := make(map[int][]byte, len(c.Keys))
	for ver, keyStr := range c.Keys {
		raw, err := ParseKey(keyStr)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to parse key for version %d: %v", ErrInvalidKey, ver, err)
		}
		decodedKeys[ver] = raw
	}

	return NewKeyRing(c.ActiveVersion, decodedKeys)
}

// ParseKey decodes an AES-256 key (must be exactly 32 bytes) from either 64-character hexadecimal
// or 32-byte base64 encoding.
func ParseKey(encoded string) ([]byte, error) {
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return nil, errors.New("key cannot be empty")
	}

	// 1. Try 64-character hex (e.g. openssl rand -hex 32)
	if len(trimmed) == 64 {
		if b, err := hex.DecodeString(trimmed); err == nil {
			return b, nil
		}
	}

	// 2. Try standard base64 or unpadded URL base64
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(b) == AES256KeySize {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil && len(b) == AES256KeySize {
		return b, nil
	}

	// If hex decoding was attempted on wrong length
	if b, err := hex.DecodeString(trimmed); err == nil {
		if len(b) != AES256KeySize {
			return nil, fmt.Errorf("key must be exactly %d bytes (256 bits), got %d bytes", AES256KeySize, len(b))
		}
		return b, nil
	}

	return nil, fmt.Errorf("key must be 64 hexadecimal characters or 32-byte base64")
}

// GenerateEncryptionKey generates a random 32-byte AES-256 key
// encoded as a 64-character lowercase hexadecimal string.
func GenerateEncryptionKey() (string, error) {
	b := make([]byte, AES256KeySize)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("security: failed to generate key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

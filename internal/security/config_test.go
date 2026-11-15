package security_test

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/security"
)

func TestParseKey(t *testing.T) {
	raw32 := make([]byte, 32)
	for i := range raw32 {
		raw32[i] = byte(i + 1)
	}

	hex64 := hex.EncodeToString(raw32)
	stdB64 := base64.StdEncoding.EncodeToString(raw32)
	urlB64 := base64.RawURLEncoding.EncodeToString(raw32)

	// 1. Hex 64 characters
	k, err := security.ParseKey(hex64)
	if err != nil {
		t.Fatalf("ParseKey with 64-char hex failed: %v", err)
	}
	if len(k) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(k))
	}

	// 2. Standard Base64 (padded)
	k, err = security.ParseKey(stdB64)
	if err != nil {
		t.Fatalf("ParseKey with std base64 failed: %v", err)
	}
	if len(k) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(k))
	}

	// 3. Unpadded URL Base64
	k, err = security.ParseKey(urlB64)
	if err != nil {
		t.Fatalf("ParseKey with raw url base64 failed: %v", err)
	}
	if len(k) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(k))
	}

	// 4. Empty key rejected
	_, err = security.ParseKey("   ")
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}

	// 5. Wrong length (31 bytes hex)
	_, err = security.ParseKey(hex.EncodeToString(raw32[:31]))
	if err == nil {
		t.Fatal("expected error for 31-byte key, got nil")
	}

	// 6. Wrong length (33 bytes hex)
	extra33 := append(raw32, 0xff)
	_, err = security.ParseKey(hex.EncodeToString(extra33))
	if err == nil {
		t.Fatal("expected error for 33-byte key, got nil")
	}

	// 7. Non-hex/base64 garbage
	_, err = security.ParseKey("!!not-valid-hex-or-base64!!")
	if err == nil {
		t.Fatal("expected error for malformed key, got nil")
	}
}

func TestEncryptionConfig_BuildKeyRing(t *testing.T) {
	key1, _ := security.GenerateEncryptionKey()
	key2, _ := security.GenerateEncryptionKey()

	// 1. Valid single key
	cfg := security.EncryptionConfig{
		ActiveVersion: 1,
		Keys: map[int]string{
			1: key1,
		},
	}
	kr, err := cfg.BuildKeyRing()
	if err != nil {
		t.Fatalf("BuildKeyRing failed: %v", err)
	}
	if kr.ActiveVersion() != 1 {
		t.Errorf("expected active version 1, got %d", kr.ActiveVersion())
	}

	// 2. Valid multi-key with active version 2
	cfgMulti := security.EncryptionConfig{
		ActiveVersion: 2,
		Keys: map[int]string{
			1: key1,
			2: key2,
		},
	}
	krMulti, err := cfgMulti.BuildKeyRing()
	if err != nil {
		t.Fatalf("BuildKeyRing multi failed: %v", err)
	}
	if krMulti.ActiveVersion() != 2 {
		t.Errorf("expected active version 2, got %d", krMulti.ActiveVersion())
	}

	// 3. Active key missing from keys map
	cfgMissingActive := security.EncryptionConfig{
		ActiveVersion: 3,
		Keys: map[int]string{
			1: key1,
			2: key2,
		},
	}
	_, err = cfgMissingActive.BuildKeyRing()
	if err == nil {
		t.Fatal("expected error when active key version is missing from keys, got nil")
	}
	if !errors.Is(err, security.ErrInvalidKey) {
		t.Errorf("expected ErrInvalidKey, got: %v", err)
	}

	// 4. Invalid active version (<= 0)
	cfgZeroVer := security.EncryptionConfig{
		ActiveVersion: 0,
		Keys:          map[int]string{1: key1},
	}
	_, err = cfgZeroVer.BuildKeyRing()
	if err == nil {
		t.Fatal("expected error for active version 0, got nil")
	}

	// 5. Empty keys map
	cfgEmptyKeys := security.EncryptionConfig{
		ActiveVersion: 1,
		Keys:          map[int]string{},
	}
	_, err = cfgEmptyKeys.BuildKeyRing()
	if err == nil {
		t.Fatal("expected error for empty keys map, got nil")
	}
}

func TestEncryptionConfigFromEnv(t *testing.T) {
	key1, _ := security.GenerateEncryptionKey()
	key2, _ := security.GenerateEncryptionKey()

	// 1. Single key setup
	t.Setenv("TOKEN_ENCRYPTION_KEY", key1)
	t.Setenv("TOKEN_ENCRYPTION_ACTIVE_KEY_VERSION", "1")
	t.Setenv("TOKEN_ENCRYPTION_KEYS", "")

	cfg := security.EncryptionConfigFromEnv()
	if cfg.ActiveVersion != 1 {
		t.Errorf("expected active version 1, got %d", cfg.ActiveVersion)
	}
	if cfg.Keys[1] != key1 {
		t.Errorf("expected key1 for version 1, got %s", cfg.Keys[1])
	}

	// 2. Comma-separated multi-key setup
	multi := "1:" + key1 + ",2:" + key2
	t.Setenv("TOKEN_ENCRYPTION_KEYS", multi)
	t.Setenv("TOKEN_ENCRYPTION_ACTIVE_KEY_VERSION", "2")

	cfgMulti := security.EncryptionConfigFromEnv()
	if cfgMulti.ActiveVersion != 2 {
		t.Errorf("expected active version 2, got %d", cfgMulti.ActiveVersion)
	}
	if cfgMulti.Keys[1] != key1 || cfgMulti.Keys[2] != key2 {
		t.Errorf("unexpected keys parsed from TOKEN_ENCRYPTION_KEYS: %v", cfgMulti.Keys)
	}
}

func TestGenerateEncryptionKey(t *testing.T) {
	k, err := security.GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey failed: %v", err)
	}

	if len(k) != 64 {
		t.Errorf("expected 64 hex characters, got %d", len(k))
	}

	raw, err := hex.DecodeString(k)
	if err != nil {
		t.Fatalf("key is not valid hex: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("expected 32 raw bytes, got %d", len(raw))
	}

	if k != strings.ToLower(k) {
		t.Error("expected lowercase hex key")
	}
}

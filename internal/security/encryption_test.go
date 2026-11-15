package security_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/security"
)

func newTestKeyRing(t *testing.T, activeVer int, keys map[int]string) *security.KeyRing {
	t.Helper()

	decoded := make(map[int][]byte, len(keys))
	for ver, kStr := range keys {
		k, err := security.ParseKey(kStr)
		if err != nil {
			t.Fatalf("failed to parse test key for version %d: %v", ver, err)
		}
		decoded[ver] = k
	}

	kr, err := security.NewKeyRing(activeVer, decoded)
	if err != nil {
		t.Fatalf("NewKeyRing failed: %v", err)
	}
	return kr
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	kr := newTestKeyRing(t, 1, map[int]string{1: keyA})
	encrypter, err := security.NewTokenEncrypter(kr)
	if err != nil {
		t.Fatalf("NewTokenEncrypter failed: %v", err)
	}

	testCases := []struct {
		name     string
		rawToken string
		aad      []byte
	}{
		{
			name:     "registration with AAD",
			rawToken: "token_reg_12345_sample_base64url",
			aad:      []byte(security.PurposeRegistrationVerification),
		},
		{
			name:     "password reset with AAD",
			rawToken: "token_pwd_67890_sample_base64url",
			aad:      []byte(security.PurposePasswordReset),
		},
		{
			name:     "without AAD (nil)",
			rawToken: "token_no_aad_example",
			aad:      nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := encrypter.EncryptToken(tc.rawToken, tc.aad)
			if err != nil {
				t.Fatalf("EncryptToken failed: %v", err)
			}

			if enc.KeyVersion != 1 {
				t.Errorf("expected KeyVersion 1, got %d", enc.KeyVersion)
			}
			if enc.Nonce == "" {
				t.Error("expected non-empty nonce")
			}
			if enc.Ciphertext == "" {
				t.Error("expected non-empty ciphertext")
			}

			decrypted, err := encrypter.DecryptToken(enc, tc.aad)
			if err != nil {
				t.Fatalf("DecryptToken failed: %v", err)
			}

			if decrypted != tc.rawToken {
				t.Errorf("expected %q, got %q", tc.rawToken, decrypted)
			}
		})
	}
}

func TestEncryptToken_DifferentNonces(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	kr := newTestKeyRing(t, 1, map[int]string{1: keyA})
	encrypter, err := security.NewTokenEncrypter(kr)
	if err != nil {
		t.Fatalf("NewTokenEncrypter failed: %v", err)
	}

	const count = 100
	const rawToken = "identical_token_value_for_nonce_testing"
	aad := []byte(security.PurposeRegistrationVerification)

	seenNonces := make(map[string]struct{}, count)
	seenCiphertexts := make(map[string]struct{}, count)

	for i := 0; i < count; i++ {
		enc, err := encrypter.EncryptToken(rawToken, aad)
		if err != nil {
			t.Fatalf("iteration %d: EncryptToken failed: %v", i, err)
		}

		if _, exists := seenNonces[enc.Nonce]; exists {
			t.Fatalf("duplicate nonce detected on iteration %d: %s", i, enc.Nonce)
		}
		seenNonces[enc.Nonce] = struct{}{}

		if _, exists := seenCiphertexts[enc.Ciphertext]; exists {
			t.Fatalf("duplicate ciphertext detected on iteration %d: %s", i, enc.Ciphertext)
		}
		seenCiphertexts[enc.Ciphertext] = struct{}{}
	}
}

func TestDecryptToken_IncorrectKey(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	keyB, _ := security.GenerateEncryptionKey()

	encrypterA, _ := security.NewTokenEncrypter(newTestKeyRing(t, 1, map[int]string{1: keyA}))
	encrypterB, _ := security.NewTokenEncrypter(newTestKeyRing(t, 1, map[int]string{1: keyB}))

	aad := []byte(security.PurposeRegistrationVerification)
	enc, err := encrypterA.EncryptToken("secret_token_123", aad)
	if err != nil {
		t.Fatalf("EncryptToken failed: %v", err)
	}

	// Attempting to decrypt with wrong key must fail with ErrDecryptionFailed
	_, err = encrypterB.DecryptToken(enc, aad)
	if err == nil {
		t.Fatal("expected decryption with wrong key to fail, but succeeded")
	}
	if !errors.Is(err, security.ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestDecryptToken_CiphertextTampering(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	encrypter, _ := security.NewTokenEncrypter(newTestKeyRing(t, 1, map[int]string{1: keyA}))

	aad := []byte(security.PurposeRegistrationVerification)
	enc, err := encrypter.EncryptToken("secret_token_123", aad)
	if err != nil {
		t.Fatalf("EncryptToken failed: %v", err)
	}

	rawCt, err := base64.StdEncoding.DecodeString(enc.Ciphertext)
	if err != nil {
		t.Fatalf("failed to decode ciphertext: %v", err)
	}

	// Flip a single bit in the ciphertext
	rawCt[0] ^= 0x01
	tamperedEnc := enc
	tamperedEnc.Ciphertext = base64.StdEncoding.EncodeToString(rawCt)

	_, err = encrypter.DecryptToken(tamperedEnc, aad)
	if err == nil {
		t.Fatal("expected bit-flipped ciphertext to fail authentication, but succeeded")
	}
	if !errors.Is(err, security.ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestDecryptToken_NonceTampering(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	encrypter, _ := security.NewTokenEncrypter(newTestKeyRing(t, 1, map[int]string{1: keyA}))

	aad := []byte(security.PurposeRegistrationVerification)
	enc, err := encrypter.EncryptToken("secret_token_123", aad)
	if err != nil {
		t.Fatalf("EncryptToken failed: %v", err)
	}

	rawNonce, err := base64.StdEncoding.DecodeString(enc.Nonce)
	if err != nil {
		t.Fatalf("failed to decode nonce: %v", err)
	}

	// Flip a bit in the nonce
	rawNonce[0] ^= 0x01
	tamperedEnc := enc
	tamperedEnc.Nonce = base64.StdEncoding.EncodeToString(rawNonce)

	_, err = encrypter.DecryptToken(tamperedEnc, aad)
	if err == nil {
		t.Fatal("expected tampered nonce to fail decryption, but succeeded")
	}
	if !errors.Is(err, security.ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed, got: %v", err)
	}

	// Invalid nonce length (truncated to 11 bytes)
	tamperedEnc.Nonce = base64.StdEncoding.EncodeToString(rawNonce[:11])
	_, err = encrypter.DecryptToken(tamperedEnc, aad)
	if err == nil {
		t.Fatal("expected truncated nonce to fail decryption, but succeeded")
	}
	if !errors.Is(err, security.ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed for truncated nonce, got: %v", err)
	}
}

func TestDecryptToken_UnknownKeyVersion(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	encrypter, _ := security.NewTokenEncrypter(newTestKeyRing(t, 1, map[int]string{1: keyA}))

	aad := []byte(security.PurposeRegistrationVerification)
	enc, err := encrypter.EncryptToken("secret_token_123", aad)
	if err != nil {
		t.Fatalf("EncryptToken failed: %v", err)
	}

	enc.KeyVersion = 99 // Not in keyring
	_, err = encrypter.DecryptToken(enc, aad)
	if err == nil {
		t.Fatal("expected unknown key version to fail, but succeeded")
	}
	if !errors.Is(err, security.ErrUnknownKeyVersion) {
		t.Errorf("expected ErrUnknownKeyVersion, got: %v", err)
	}
}

func TestKeyRotation_HistoricalDecryption(t *testing.T) {
	keyV1, _ := security.GenerateEncryptionKey()
	keyV2, _ := security.GenerateEncryptionKey()

	// Initial phase: Ring has only key v1
	krInitial := newTestKeyRing(t, 1, map[int]string{1: keyV1})
	encrypterInitial, _ := security.NewTokenEncrypter(krInitial)

	aad := []byte(security.PurposeRegistrationVerification)
	oldToken := "old_registration_token_v1"
	encOld, err := encrypterInitial.EncryptToken(oldToken, aad)
	if err != nil {
		t.Fatalf("EncryptToken v1 failed: %v", err)
	}
	if encOld.KeyVersion != 1 {
		t.Errorf("expected KeyVersion 1, got %d", encOld.KeyVersion)
	}

	// Rotation phase: Active is v2, but v1 is retained as historical key
	krRotated := newTestKeyRing(t, 2, map[int]string{
		1: keyV1,
		2: keyV2,
	})
	encrypterRotated, _ := security.NewTokenEncrypter(krRotated)

	if encrypterRotated.ActiveKeyVersion() != 2 {
		t.Errorf("expected active key version 2, got %d", encrypterRotated.ActiveKeyVersion())
	}

	// 1. Historical token encrypted under v1 can still be decrypted
	decryptedOld, err := encrypterRotated.DecryptToken(encOld, aad)
	if err != nil {
		t.Fatalf("failed to decrypt old token with rotated key ring: %v", err)
	}
	if decryptedOld != oldToken {
		t.Errorf("expected %q, got %q", oldToken, decryptedOld)
	}

	// 2. New token encrypted under v2 uses active key v2
	newToken := "new_registration_token_v2"
	encNew, err := encrypterRotated.EncryptToken(newToken, aad)
	if err != nil {
		t.Fatalf("EncryptToken v2 failed: %v", err)
	}
	if encNew.KeyVersion != 2 {
		t.Errorf("expected new token KeyVersion 2, got %d", encNew.KeyVersion)
	}

	// 3. New token decrypts successfully
	decryptedNew, err := encrypterRotated.DecryptToken(encNew, aad)
	if err != nil {
		t.Fatalf("failed to decrypt new token: %v", err)
	}
	if decryptedNew != newToken {
		t.Errorf("expected %q, got %q", newToken, decryptedNew)
	}
}

func TestDecryptToken_MismatchedAAD(t *testing.T) {
	keyA, _ := security.GenerateEncryptionKey()
	encrypter, _ := security.NewTokenEncrypter(newTestKeyRing(t, 1, map[int]string{1: keyA}))

	rawToken := "purpose_bound_token"
	regAAD := []byte(security.PurposeRegistrationVerification)
	pwdAAD := []byte(security.PurposePasswordReset)

	enc, err := encrypter.EncryptToken(rawToken, regAAD)
	if err != nil {
		t.Fatalf("EncryptToken failed: %v", err)
	}

	// 1. Attempt decrypt with wrong AAD (password reset instead of registration)
	_, err = encrypter.DecryptToken(enc, pwdAAD)
	if err == nil {
		t.Fatal("expected decryption with mismatched AAD to fail, but succeeded")
	}
	if !errors.Is(err, security.ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed, got: %v", err)
	}

	// 2. Attempt decrypt with nil AAD
	_, err = encrypter.DecryptToken(enc, nil)
	if err == nil {
		t.Fatal("expected decryption with nil AAD to fail, but succeeded")
	}
	if !errors.Is(err, security.ErrDecryptionFailed) {
		t.Errorf("expected ErrDecryptionFailed, got: %v", err)
	}

	// 3. Decrypt with correct AAD succeeds
	decrypted, err := encrypter.DecryptToken(enc, regAAD)
	if err != nil {
		t.Fatalf("DecryptToken with matching AAD failed: %v", err)
	}
	if decrypted != rawToken {
		t.Errorf("expected %q, got %q", rawToken, decrypted)
	}
}

func TestEncryptedToken_String_Redacted(t *testing.T) {
	enc := security.EncryptedToken{
		KeyVersion: 1,
		Nonce:      "sensitive_nonce_bytes",
		Ciphertext: "sensitive_ciphertext_payload",
	}

	str := enc.String()
	if strings.Contains(str, "sensitive_nonce") || strings.Contains(str, "sensitive_ciphertext") {
		t.Errorf("String() exposes sensitive nonce or ciphertext: %s", str)
	}
	if str != "EncryptedToken(v1)" {
		t.Errorf("unexpected String() output: %s", str)
	}
}

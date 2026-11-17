package auth_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestService_RequestPasswordReset_ExistingUser(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "reset_existing@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "InitialPassword1234!")

	err := service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("RequestPasswordReset failed: %v", err)
	}

	// 1. Verify token in database
	var tokenHash string
	var usedAt *time.Time
	var expiresAt time.Time
	err = pool.QueryRow(ctx, `
		SELECT prt.token_hash, prt.used_at, prt.expires_at
		FROM password_reset_tokens prt
		JOIN users u ON u.id = prt.user_id
		WHERE u.email = $1
	`, email).Scan(&tokenHash, &usedAt, &expiresAt)
	if err != nil {
		t.Fatalf("failed to query password_reset_tokens: %v", err)
	}

	if len(tokenHash) != 64 {
		t.Errorf("expected 64-char hex SHA-256 token hash, got %d chars", len(tokenHash))
	}
	if usedAt != nil {
		t.Errorf("expected token used_at to be nil initially")
	}
	if expiresAt.Before(time.Now().Add(19 * time.Minute)) {
		t.Errorf("expected ~20m expiration, got %v", expiresAt)
	}

	// 2. Verify outbox event
	var eventType, payloadJSON string
	err = pool.QueryRow(ctx, `
		SELECT event_type, payload::text
		FROM outbox_events
		WHERE aggregate_type = 'password_reset'
	`).Scan(&eventType, &payloadJSON)
	if err != nil {
		t.Fatalf("failed to query outbox event: %v", err)
	}

	if eventType != outbox.EventTypePasswordReset {
		t.Errorf("expected event_type %s, got %s", outbox.EventTypePasswordReset, eventType)
	}

	payload, err := outbox.ParseEmailPayload([]byte(payloadJSON))
	if err != nil {
		t.Fatalf("failed to parse outbox payload: %v", err)
	}
	if payload.Recipient != email {
		t.Errorf("expected payload recipient %s, got %s", email, payload.Recipient)
	}

	// 3. Verify encrypted token decrypts using password reset AAD purpose
	rawToken, err := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))
	if err != nil {
		t.Fatalf("failed to decrypt outbox token with password reset purpose: %v", err)
	}
	if security.HashToken(rawToken) != tokenHash {
		t.Errorf("hash of decrypted token does not match persisted token_hash")
	}

	// 4. Verify raw token does NOT decrypt using registration verification AAD purpose
	_, err = encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))
	if err == nil {
		t.Error("expected decryption failure when using wrong AAD purpose (registration verification)")
	}
}

func TestService_RequestPasswordReset_NonexistentUser(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "nobody_exists@example.com"

	// Request for non-existent user must return nil (generic response)
	err := service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("expected nil for nonexistent user, got error: %v", err)
	}

	// Verify NO password reset tokens were created
	var tokenCount int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM password_reset_tokens").Scan(&tokenCount)
	if tokenCount != 0 {
		t.Errorf("expected 0 tokens for nonexistent user, got %d", tokenCount)
	}

	// Verify NO outbox events were created
	var outboxCount int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&outboxCount)
	if outboxCount != 0 {
		t.Errorf("expected 0 outbox events for nonexistent user, got %d", outboxCount)
	}
}

func TestService_RequestPasswordReset_Cooldown(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "reset_cooldown@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	// First request succeeds
	err := service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	// Immediate second request must trigger cooldown
	err = service.RequestPasswordReset(ctx, email)
	if !errors.Is(err, auth.ErrCooldownActive) {
		t.Fatalf("expected ErrCooldownActive, got %v", err)
	}

	// Short cooldown for test
	service.SetCooldown(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	err = service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("request after cooldown failed: %v", err)
	}

	var count int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM password_reset_tokens").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 password reset tokens, got %d", count)
	}
}

func TestService_RequestPasswordReset_ConcurrentRequests(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "concurrent_reset@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	numRequests := 10
	var wg sync.WaitGroup
	var successCount, cooldownCount int
	var mu sync.Mutex

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := service.RequestPasswordReset(ctx, email)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if errors.Is(err, auth.ErrCooldownActive) {
				cooldownCount++
			}
		}()
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 concurrent request to succeed, got %d", successCount)
	}
	if cooldownCount != numRequests-1 {
		t.Errorf("expected %d requests to hit cooldown, got %d", numRequests-1, cooldownCount)
	}

	var totalTokens int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM password_reset_tokens").Scan(&totalTokens)
	if totalTokens != 1 {
		t.Errorf("expected exactly 1 token in database, got %d", totalTokens)
	}
}

func TestService_CompletePasswordReset_Success(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "complete_reset@example.com"
	oldPassword := "OldSecurePassword123!"
	newPassword := "NewSecurePassword123!"

	createVerifiedUser(t, pool, service, encrypter, email, oldPassword)

	// Create 2 sessions for the user to verify revocation
	_, _, err := service.Login(ctx, email, oldPassword)
	if err != nil {
		t.Fatalf("first login failed: %v", err)
	}
	_, _, err = service.Login(ctx, email, oldPassword)
	if err != nil {
		t.Fatalf("second login failed: %v", err)
	}

	var activeSessionsBefore int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&activeSessionsBefore)
	if activeSessionsBefore != 2 {
		t.Fatalf("expected 2 active sessions before reset, got %d", activeSessionsBefore)
	}

	// 1. Request password reset
	err = service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("RequestPasswordReset failed: %v", err)
	}

	// 2. Fetch raw token from outbox payload
	var payloadJSON []byte
	err = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'password_reset' ORDER BY occurred_at DESC LIMIT 1").Scan(&payloadJSON)
	if err != nil {
		t.Fatalf("failed to query outbox: %v", err)
	}
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, err := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))
	if err != nil {
		t.Fatalf("failed to decrypt reset token: %v", err)
	}

	// 3. Complete password reset
	err = service.CompletePasswordReset(ctx, rawToken, newPassword)
	if err != nil {
		t.Fatalf("CompletePasswordReset failed: %v", err)
	}

	// 4. Verify password hash was updated
	var newHash string
	err = pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE email = $1", email).Scan(&newHash)
	if err != nil {
		t.Fatalf("failed to query user: %v", err)
	}
	match, err := security.VerifyPassword(newPassword, newHash)
	if err != nil || !match {
		t.Errorf("new password verification failed: %v", err)
	}
	oldMatch, _ := security.VerifyPassword(oldPassword, newHash)
	if oldMatch {
		t.Error("old password should not match updated password hash")
	}

	// 5. Verify reset token is marked used
	var usedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT used_at FROM password_reset_tokens WHERE token_hash = $1", security.HashToken(rawToken)).Scan(&usedAt)
	if err != nil || usedAt == nil {
		t.Errorf("expected reset token to be marked used")
	}

	// 6. Verify all existing sessions are revoked
	var activeSessionsAfter int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&activeSessionsAfter)
	if activeSessionsAfter != 0 {
		t.Errorf("expected 0 active sessions after password reset, got %d", activeSessionsAfter)
	}

	var revokedSessions int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE revoked_at IS NOT NULL").Scan(&revokedSessions)
	if revokedSessions != 2 {
		t.Errorf("expected 2 revoked sessions, got %d", revokedSessions)
	}

	// 7. Verify login with new password succeeds
	_, _, err = service.Login(ctx, email, newPassword)
	if err != nil {
		t.Fatalf("login with new password failed: %v", err)
	}

	// 8. Verify login with old password fails
	_, _, err = service.Login(ctx, email, oldPassword)
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for old password, got %v", err)
	}
}

func TestService_CompletePasswordReset_InvalidToken(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	err := service.CompletePasswordReset(ctx, "nonexistent-reset-token", "NewPassword1234!")
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestService_CompletePasswordReset_ExpiredToken(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "expired_reset@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	// Set expiration in the past
	service.SetPasswordResetTokenExpiry(-1 * time.Minute)

	err := service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("RequestPasswordReset failed: %v", err)
	}

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'password_reset' ORDER BY occurred_at DESC LIMIT 1").Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))

	err = service.CompletePasswordReset(ctx, rawToken, "NewPassword1234!")
	if !errors.Is(err, auth.ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestService_CompletePasswordReset_AlreadyConsumedToken(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "consumed_reset@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	_ = service.RequestPasswordReset(ctx, email)

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'password_reset' ORDER BY occurred_at DESC LIMIT 1").Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))

	// First completion succeeds
	err := service.CompletePasswordReset(ctx, rawToken, "NewPassword1234!")
	if err != nil {
		t.Fatalf("first CompletePasswordReset failed: %v", err)
	}

	// Second completion with same token fails
	err = service.CompletePasswordReset(ctx, rawToken, "AnotherPassword123!")
	if !errors.Is(err, auth.ErrTokenAlreadyConsumed) {
		t.Errorf("expected ErrTokenAlreadyConsumed, got %v", err)
	}
}

func TestService_CompletePasswordReset_CrossPurpose_RegistrationTokenCannotReset(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	// 1. Request registration to produce a registration verification token
	email := "cross_reg@example.com"
	err := service.RequestRegistration(ctx, email)
	if err != nil {
		t.Fatalf("RequestRegistration failed: %v", err)
	}

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'registration_challenge' LIMIT 1").Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawRegToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	// 2. Attempt to use registration token for password reset
	err = service.CompletePasswordReset(ctx, rawRegToken, "NewPassword1234!")
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken when using registration token for password reset, got %v", err)
	}
}

func TestService_CompleteRegistration_CrossPurpose_PasswordResetTokenCannotRegister(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "cross_reset@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	// 1. Request password reset to produce a reset token
	err := service.RequestPasswordReset(ctx, email)
	if err != nil {
		t.Fatalf("RequestPasswordReset failed: %v", err)
	}

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'password_reset' LIMIT 1").Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawResetToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))

	// 2. Attempt to use password reset token for registration completion
	_, err = service.CompleteRegistration(ctx, rawResetToken, "NewPassword1234!", "User")
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken when using password reset token for registration, got %v", err)
	}
}

func TestService_CompletePasswordReset_ConcurrentAttempts(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "concurrent_complete_reset@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	_ = service.RequestPasswordReset(ctx, email)

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'password_reset' LIMIT 1").Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))

	numWorkers := 5
	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := service.CompletePasswordReset(ctx, rawToken, fmt.Sprintf("NewPassword%d_1234!", idx))
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 concurrent completion to succeed, got %d", successCount)
	}
}

func TestService_CompletePasswordReset_WeakPasswordRejected_TokenUnconsumed(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "weak_pw_reset@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "Password1234!")

	_ = service.RequestPasswordReset(ctx, email)

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE aggregate_type = 'password_reset' LIMIT 1").Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))

	// Short password (< 12 chars)
	err := service.CompletePasswordReset(ctx, rawToken, "short")
	if !errors.Is(err, auth.ErrInvalidPassword) {
		t.Errorf("expected ErrInvalidPassword, got %v", err)
	}

	// Verify token is STILL unconsumed in database
	var usedAt pgtype.Timestamptz
	_ = pool.QueryRow(ctx, "SELECT used_at FROM password_reset_tokens WHERE token_hash = $1", security.HashToken(rawToken)).Scan(&usedAt)
	if usedAt.Valid {
		t.Error("expected token to remain unconsumed after weak password rejection")
	}

	// Completing with a strong password should now succeed
	err = service.CompletePasswordReset(ctx, rawToken, "ValidNewPassword1234!")
	if err != nil {
		t.Fatalf("subsequent CompletePasswordReset failed: %v", err)
	}
}

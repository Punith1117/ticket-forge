package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupIntegrationDB(t *testing.T) (*pgxpool.Pool, *auth.Service, *security.TokenEncrypter) {
	t.Helper()

	config.Load("../../.env")

	cfg := database.ConfigFromEnv()
	testDB := os.Getenv("TEST_POSTGRES_DB")
	if testDB == "" {
		testDB = "ticketforge_test"
	}
	cfg.Database = testDB

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		t.Skipf("skipping PostgreSQL integration test (database unreachable: %v)", err)
		return nil, nil, nil
	}

	// Clean test tables
	_, err = pool.Exec(ctx, `
		DELETE FROM password_reset_tokens;
		DELETE FROM sessions;
		DELETE FROM users;
		DELETE FROM registration_verification_challenges;
		DELETE FROM outbox_events;
	`)
	if err != nil {
		pool.Close()
		t.Fatalf("failed to clean test tables: %v", err)
	}

	encrypter := newTestEncrypter(t)
	service, err := auth.NewService(pool, encrypter)
	if err != nil {
		pool.Close()
		t.Fatalf("failed to create auth service: %v", err)
	}

	return pool, service, encrypter
}

func TestService_RequestRegistration_ValidEmail(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "  Alice.Smith@Example.COM  "

	err := service.RequestRegistration(ctx, email)
	if err != nil {
		t.Fatalf("RequestRegistration failed: %v", err)
	}

	// 1. Verify challenge in database
	var challengeEmail, tokenHash string
	var consumedAt *time.Time
	var expiresAt time.Time
	err = pool.QueryRow(ctx, `
		SELECT email, token_hash, consumed_at, expires_at
		FROM registration_verification_challenges
		WHERE email = 'alice.smith@example.com'
	`).Scan(&challengeEmail, &tokenHash, &consumedAt, &expiresAt)
	if err != nil {
		t.Fatalf("failed to query challenge: %v", err)
	}

	if challengeEmail != "alice.smith@example.com" {
		t.Errorf("expected normalized email alice.smith@example.com, got %s", challengeEmail)
	}
	if len(tokenHash) != 64 {
		t.Errorf("expected 64-char hex SHA-256 token hash, got %d chars", len(tokenHash))
	}
	if consumedAt != nil {
		t.Errorf("expected challenge consumed_at to be nil initially")
	}
	if expiresAt.Before(time.Now().Add(23 * time.Hour)) {
		t.Errorf("expected 24h expiration, got %v", expiresAt)
	}

	// 2. Verify outbox event
	var eventType, payloadJSON string
	err = pool.QueryRow(ctx, `
		SELECT event_type, payload::text
		FROM outbox_events
		WHERE aggregate_type = 'registration_challenge'
	`).Scan(&eventType, &payloadJSON)
	if err != nil {
		t.Fatalf("failed to query outbox event: %v", err)
	}

	if eventType != outbox.EventTypeRegistrationVerification {
		t.Errorf("expected event_type %s, got %s", outbox.EventTypeRegistrationVerification, eventType)
	}

	payload, err := outbox.ParseEmailPayload([]byte(payloadJSON))
	if err != nil {
		t.Fatalf("failed to parse outbox payload: %v", err)
	}
	if payload.Recipient != "alice.smith@example.com" {
		t.Errorf("expected payload recipient alice.smith@example.com, got %s", payload.Recipient)
	}

	// 3. Verify encrypted token decrypts using registration AAD
	rawToken, err := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))
	if err != nil {
		t.Fatalf("failed to decrypt outbox token: %v", err)
	}
	if security.HashToken(rawToken) != tokenHash {
		t.Errorf("hash of decrypted token does not match persisted challenge token hash")
	}
}

func TestService_RequestRegistration_GenericResponseForExistingUser(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	existingEmail := "existing@example.com"

	// Pre-create user account
	_, err := pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Existing User')
	`, existingEmail)
	if err != nil {
		t.Fatalf("failed to insert existing user: %v", err)
	}

	// Request registration for existing user -> must return nil (generic response)
	err = service.RequestRegistration(ctx, existingEmail)
	if err != nil {
		t.Fatalf("expected generic success for existing user, got error: %v", err)
	}

	// Ensure NO challenges were created
	var challengeCount int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM registration_verification_challenges WHERE email = $1", existingEmail).Scan(&challengeCount)
	if challengeCount != 0 {
		t.Errorf("expected 0 challenges for existing user, got %d", challengeCount)
	}

	// Ensure NO outbox events were created
	var outboxCount int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&outboxCount)
	if outboxCount != 0 {
		t.Errorf("expected 0 outbox events for existing user, got %d", outboxCount)
	}
}

func TestService_RequestRegistration_ResendCooldown(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "cooldown@example.com"

	// First request succeeds
	err := service.RequestRegistration(ctx, email)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	// Immediate second request must trigger cooldown
	err = service.RequestRegistration(ctx, email)
	if !errors.Is(err, auth.ErrCooldownActive) {
		t.Fatalf("expected ErrCooldownActive, got %v", err)
	}
}

func TestService_RequestRegistration_MultipleOutstandingChallenges(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "multi@example.com"

	// Use tiny cooldown for test
	service.SetCooldown(10 * time.Millisecond)

	err := service.RequestRegistration(ctx, email)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	err = service.RequestRegistration(ctx, email)
	if err != nil {
		t.Fatalf("second request after cooldown failed: %v", err)
	}

	var count int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM registration_verification_challenges WHERE email = $1", email).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 outstanding challenges, got %d", count)
	}
}

func TestService_RequestRegistration_ConcurrentRequests(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "concurrent@example.com"

	numRequests := 10
	var wg sync.WaitGroup
	var successCount, cooldownCount int
	var mu sync.Mutex

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := service.RequestRegistration(ctx, email)
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

	var totalChallenges int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM registration_verification_challenges WHERE email = $1", email).Scan(&totalChallenges)
	if totalChallenges != 1 {
		t.Errorf("expected exactly 1 challenge in database, got %d", totalChallenges)
	}
}

func TestService_CompleteRegistration_Success(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "complete@example.com"
	rawPassword := "SecureP@ssw0rd1234!"

	// 1. Request registration
	err := service.RequestRegistration(ctx, email)
	if err != nil {
		t.Fatalf("RequestRegistration failed: %v", err)
	}

	// 2. Fetch raw token from outbox payload
	var payloadJSON []byte
	err = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE payload->>'recipient' = $1", email).Scan(&payloadJSON)
	if err != nil {
		t.Fatalf("failed to query outbox: %v", err)
	}
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, err := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))
	if err != nil {
		t.Fatalf("failed to decrypt token: %v", err)
	}

	// 3. Complete registration
	resp, err := service.CompleteRegistration(ctx, rawToken, rawPassword, "Complete User")
	if err != nil {
		t.Fatalf("CompleteRegistration failed: %v", err)
	}

	if resp.Email != email {
		t.Errorf("expected email %s, got %s", email, resp.Email)
	}
	if resp.DisplayName != "Complete User" {
		t.Errorf("expected display name 'Complete User', got %s", resp.DisplayName)
	}
	if resp.ID == "" {
		t.Errorf("expected non-empty user ID")
	}

	// 4. Verify user in database
	var dbPasswordHash string
	var verifiedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT password_hash, email_verified_at FROM users WHERE email = $1", email).Scan(&dbPasswordHash, &verifiedAt)
	if err != nil {
		t.Fatalf("failed to query user: %v", err)
	}

	if verifiedAt == nil {
		t.Error("expected email_verified_at to be set")
	}

	// 5. Verify Argon2id password hash
	match, err := security.VerifyPassword(rawPassword, dbPasswordHash)
	if err != nil || !match {
		t.Errorf("password verification against stored hash failed: %v", err)
	}

	// Ensure raw password is NOT stored
	if dbPasswordHash == rawPassword {
		t.Error("CRITICAL: raw password stored in database!")
	}

	// 6. Verify challenge is marked consumed
	var consumedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT consumed_at FROM registration_verification_challenges WHERE email = $1", email).Scan(&consumedAt)
	if err != nil || consumedAt == nil {
		t.Errorf("expected challenge to be marked consumed")
	}
}

func TestService_CompleteRegistration_InvalidToken(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()

	_, err := service.CompleteRegistration(ctx, "nonexistent-token-abc", "SecureP@ssw0rd1234!", "User")
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestService_CompleteRegistration_ExpiredToken(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	service.SetTokenExpiry(-1 * time.Minute) // Already expired

	email := "expired@example.com"
	_ = service.RequestRegistration(ctx, email)

	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE payload->>'recipient' = $1", email).Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	_, err := service.CompleteRegistration(ctx, rawToken, "SecureP@ssw0rd1234!", "User")
	if !errors.Is(err, auth.ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestService_CompleteRegistration_AlreadyConsumedToken(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "consumed@example.com"

	_ = service.RequestRegistration(ctx, email)
	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE payload->>'recipient' = $1", email).Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	// First completion succeeds
	_, err := service.CompleteRegistration(ctx, rawToken, "SecureP@ssw0rd1234!", "User")
	if err != nil {
		t.Fatalf("first completion failed: %v", err)
	}

	// Second completion with same token fails
	_, err = service.CompleteRegistration(ctx, rawToken, "SecureP@ssw0rd1234!", "User")
	if !errors.Is(err, auth.ErrTokenAlreadyConsumed) {
		t.Errorf("expected ErrTokenAlreadyConsumed, got %v", err)
	}
}

func TestService_CompleteRegistration_ConcurrentSameToken(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "concurrent_complete@example.com"

	_ = service.RequestRegistration(ctx, email)
	var payloadJSON []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events WHERE payload->>'recipient' = $1", email).Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	numWorkers := 5
	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			_, err := service.CompleteRegistration(ctx, rawToken, "SecureP@ssw0rd1234!", fmt.Sprintf("User %d", workerID))
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 completion to succeed, got %d", successCount)
	}

	// Verify exactly 1 user row in database
	var userCount int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = $1", email).Scan(&userCount)
	if userCount != 1 {
		t.Errorf("expected exactly 1 user row in database, got %d", userCount)
	}
}

func TestService_CompleteRegistration_DuplicateEmail(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()
	email := "dup@example.com"
	service.SetCooldown(10 * time.Millisecond)

	// Challenge 1
	_ = service.RequestRegistration(ctx, email)
	var p1 []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events ORDER BY occurred_at DESC LIMIT 1").Scan(&p1)
	payload1, _ := outbox.ParseEmailPayload(p1)
	token1, _ := encrypter.DecryptToken(payload1.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	time.Sleep(15 * time.Millisecond)

	// Challenge 2 for same email
	_ = service.RequestRegistration(ctx, email)
	var p2 []byte
	_ = pool.QueryRow(ctx, "SELECT payload FROM outbox_events ORDER BY occurred_at DESC LIMIT 1").Scan(&p2)
	payload2, _ := outbox.ParseEmailPayload(p2)
	token2, _ := encrypter.DecryptToken(payload2.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	// User completes with token 1 -> succeeds
	_, err := service.CompleteRegistration(ctx, token1, "InitialPassword1234!", "User 1")
	if err != nil {
		t.Fatalf("token 1 completion failed: %v", err)
	}

	// User attempts to complete with token 2 for same email -> fails with ErrEmailAlreadyRegistered
	_, err = service.CompleteRegistration(ctx, token2, "AttemptedNewPassword1234!", "User 2")
	if !errors.Is(err, auth.ErrEmailAlreadyRegistered) {
		t.Errorf("expected ErrEmailAlreadyRegistered, got %v", err)
	}

	// Ensure original password is preserved
	var storedHash string
	_ = pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE email = $1", email).Scan(&storedHash)
	match, err := security.VerifyPassword("InitialPassword1234!", storedHash)
	if err != nil || !match {
		t.Error("CRITICAL: initial user password was overwritten or corrupted!")
	}
}

func TestHTTP_FullRegistrationWorkflow(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	handler := auth.NewHandler(service, nil, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	email := "e2e_http@example.com"

	// 1. Request registration via HTTP POST
	reqBody, _ := json.Marshal(auth.RegisterRequest{Email: email})
	resp, err := client.Post(ts.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /register failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", resp.StatusCode)
	}

	// 2. Retrieve token from outbox
	var payloadJSON []byte
	_ = pool.QueryRow(context.Background(), "SELECT payload FROM outbox_events WHERE payload->>'recipient' = $1", email).Scan(&payloadJSON)
	payload, _ := outbox.ParseEmailPayload(payloadJSON)
	rawToken, _ := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))

	// 3. Test that GET /api/v1/auth/register/complete is rejected (Method Not Allowed)
	getResp, err := client.Get(ts.URL + "/api/v1/auth/register/complete")
	if err != nil {
		t.Fatalf("GET /complete failed: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed for GET /complete, got %d", getResp.StatusCode)
	}

	// 4. Complete registration via HTTP POST
	compReq, _ := json.Marshal(auth.RegisterCompleteRequest{
		Token:       rawToken,
		Password:    "SuperSecurePass1234!",
		DisplayName: "E2E User",
	})
	postResp, err := client.Post(ts.URL+"/api/v1/auth/register/complete", "application/json", bytes.NewReader(compReq))
	if err != nil {
		t.Fatalf("POST /complete failed: %v", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", postResp.StatusCode)
	}

	var compResult auth.RegisterCompleteResponse
	if err := json.NewDecoder(postResp.Body).Decode(&compResult); err != nil {
		t.Fatalf("failed to decode complete response: %v", err)
	}

	if compResult.User.Email != email {
		t.Errorf("expected email %s, got %s", email, compResult.User.Email)
	}
	if compResult.User.DisplayName != "E2E User" {
		t.Errorf("expected display name 'E2E User', got %s", compResult.User.DisplayName)
	}
}

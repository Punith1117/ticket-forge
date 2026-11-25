package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
)

func TestHandler_PasswordResetRequest_InvalidJSON(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request", bytes.NewBufferString("{invalid-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandlePasswordResetRequest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	var errResp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != "INVALID_REQUEST" {
		t.Errorf("expected error 'INVALID_REQUEST', got '%s'", errResp.Error)
	}
}

func TestHandler_PasswordResetRequest_InvalidEmail(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request", mustMarshal(t, auth.PasswordResetRequest{Email: "not-an-email"}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandlePasswordResetRequest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}
}

func TestHandler_PasswordResetComplete_InvalidJSON(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/complete", bytes.NewBufferString("{invalid-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandlePasswordResetComplete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}
}

func TestHandler_PasswordResetComplete_EmptyToken(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/complete", mustMarshal(t, auth.PasswordResetCompleteRequest{
		Token:    "   ",
		Password: "ValidNewPassword123!",
	}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandlePasswordResetComplete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Error.Code != "INVALID_TOKEN" {
		t.Errorf("expected error 'INVALID_TOKEN', got '%s'", errResp.Error)
	}
}

func TestHandler_PasswordResetComplete_InvalidPassword(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/complete", mustMarshal(t, auth.PasswordResetCompleteRequest{
		Token:    "some-token-value",
		Password: "short",
	}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandlePasswordResetComplete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Error.Code != "INVALID_PASSWORD" {
		t.Errorf("expected error 'INVALID_PASSWORD', got '%s'", errResp.Error)
	}
}

// --- Anti-enumeration: Existing, Nonexistent, and Cooldown must return identical HTTP responses ---

func TestHandler_PasswordResetRequest_AntiEnumeration(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		t.Skip("PostgreSQL unavailable")
	}
	defer pool.Close()

	existingEmail := "enum_test@example.com"
	createVerifiedUser(t, pool, service, encrypter, existingEmail, "Password1234!")

	handler := auth.NewHandler(service, nil, false)

	// 1. Request for existing user
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request",
		mustMarshal(t, auth.PasswordResetRequest{Email: existingEmail}))
	req1.Header.Set("Content-Type", "application/json")
	handler.HandlePasswordResetRequest(rec1, req1)

	if rec1.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for existing user, got %d", rec1.Code)
	}

	// 2. Immediate second request for existing user (cooldown situation)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request",
		mustMarshal(t, auth.PasswordResetRequest{Email: existingEmail}))
	req2.Header.Set("Content-Type", "application/json")
	handler.HandlePasswordResetRequest(rec2, req2)

	if rec2.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for cooldown request, got %d", rec2.Code)
	}

	// 3. Request for nonexistent user
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/request",
		mustMarshal(t, auth.PasswordResetRequest{Email: "nobody_here@example.com"}))
	req3.Header.Set("Content-Type", "application/json")
	handler.HandlePasswordResetRequest(rec3, req3)

	if rec3.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for nonexistent user, got %d", rec3.Code)
	}

	// All three response bodies must be completely identical
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("cooldown response body differs from initial response: %q vs %q", rec2.Body.String(), rec1.Body.String())
	}
	if rec1.Body.String() != rec3.Body.String() {
		t.Errorf("nonexistent user response body differs from existing user response: %q vs %q", rec3.Body.String(), rec1.Body.String())
	}
}

// --- Rate Limiting on Password Reset Request ---

func TestHandler_PasswordResetRequest_RateLimit_IP(t *testing.T) {
	retryAfter := 45 * time.Second
	stub := &stubLimiter{responses: []stubResult{denyResult(retryAfter)}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandlePasswordResetRequest, "/api/v1/auth/password-reset/request",
		auth.PasswordResetRequest{Email: "user@example.com"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	val := rec.Header().Get("Retry-After")
	if val != "45" {
		t.Errorf("expected Retry-After=45, got %q", val)
	}
	if stub.calls != 1 {
		t.Errorf("expected 1 rate limit call (IP only), got %d", stub.calls)
	}
}

func TestHandler_PasswordResetRequest_RateLimit_Email(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{
		allowResult(),                 // IP allows
		denyResult(120 * time.Second), // Email denies
	}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandlePasswordResetRequest, "/api/v1/auth/password-reset/request",
		auth.PasswordResetRequest{Email: "user@example.com"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	val := rec.Header().Get("Retry-After")
	if val != "120" {
		t.Errorf("expected Retry-After=120, got %q", val)
	}
	if stub.calls != 2 {
		t.Errorf("expected 2 rate limit calls (IP + email), got %d", stub.calls)
	}
}

func TestHandler_PasswordResetRequest_RateLimit_InfraFailure(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{errResult()}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandlePasswordResetRequest, "/api/v1/auth/password-reset/request",
		auth.PasswordResetRequest{Email: "user@example.com"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on rate limit infra failure, got %d", rec.Code)
	}
}

// --- Rate Limiting on Password Reset Complete ---

func TestHandler_PasswordResetComplete_RateLimit_IP(t *testing.T) {
	retryAfter := 30 * time.Second
	stub := &stubLimiter{responses: []stubResult{denyResult(retryAfter)}}
	// nil service proves handler returns before any service logic or Argon2id
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandlePasswordResetComplete, "/api/v1/auth/password-reset/complete",
		auth.PasswordResetCompleteRequest{Token: "valid-token-str", Password: "ValidPassword123!"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "30" {
		t.Errorf("expected Retry-After=30, got %q", rec.Header().Get("Retry-After"))
	}
	if stub.calls != 1 {
		t.Errorf("expected 1 rate limit call (IP only), got %d", stub.calls)
	}
}

func TestHandler_PasswordResetComplete_RateLimit_Token(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{
		allowResult(),                // IP allows
		denyResult(60 * time.Second), // Token denies
	}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandlePasswordResetComplete, "/api/v1/auth/password-reset/complete",
		auth.PasswordResetCompleteRequest{Token: "valid-token-str", Password: "ValidPassword123!"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Errorf("expected Retry-After=60, got %q", rec.Header().Get("Retry-After"))
	}
	if stub.calls != 2 {
		t.Errorf("expected 2 rate limit calls (IP + token), got %d", stub.calls)
	}
}

func TestHandler_PasswordResetComplete_RateLimit_InfraFailure(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{errResult()}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandlePasswordResetComplete, "/api/v1/auth/password-reset/complete",
		auth.PasswordResetCompleteRequest{Token: "valid-token-str", Password: "ValidPassword123!"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on limiter infra failure, got %d", rec.Code)
	}
}

// --- End-to-End Full Password Reset HTTP Workflow ---

func TestHTTP_FullPasswordResetWorkflow(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		t.Skip("PostgreSQL unavailable")
	}
	defer pool.Close()

	handler := auth.NewHandler(service, nil, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	email := "e2e_reset@example.com"
	oldPassword := "OldPassword1234!"
	newPassword := "NewPassword1234!"

	createVerifiedUser(t, pool, service, encrypter, email, oldPassword)

	// 1. Log in to establish an active session
	loginBody := mustMarshal(t, auth.LoginRequest{Email: email, Password: oldPassword})
	loginResp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on login, got %d", loginResp.StatusCode)
	}

	var activeSessionsBefore int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&activeSessionsBefore)
	if activeSessionsBefore != 1 {
		t.Fatalf("expected 1 active session, got %d", activeSessionsBefore)
	}

	// 2. Request password reset via HTTP POST
	reqBody := mustMarshal(t, auth.PasswordResetRequest{Email: email})
	reqResp, err := client.Post(ts.URL+"/api/v1/auth/password-reset/request", "application/json", reqBody)
	if err != nil {
		t.Fatalf("POST /password-reset/request failed: %v", err)
	}
	defer reqResp.Body.Close()

	if reqResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", reqResp.StatusCode)
	}

	// 3. Fetch token from outbox
	var payloadJSON []byte
	err = pool.QueryRow(context.Background(), `
		SELECT payload FROM outbox_events
		WHERE aggregate_type = 'password_reset'
		ORDER BY occurred_at DESC LIMIT 1
	`).Scan(&payloadJSON)
	if err != nil {
		t.Fatalf("failed to query outbox: %v", err)
	}
	payload, err := outbox.ParseEmailPayload(payloadJSON)
	if err != nil {
		t.Fatalf("failed to parse outbox payload: %v", err)
	}
	rawToken, err := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposePasswordReset))
	if err != nil {
		t.Fatalf("failed to decrypt reset token: %v", err)
	}

	// 4. Complete password reset via HTTP POST
	compBody := mustMarshal(t, auth.PasswordResetCompleteRequest{
		Token:    rawToken,
		Password: newPassword,
	})
	compResp, err := client.Post(ts.URL+"/api/v1/auth/password-reset/complete", "application/json", compBody)
	if err != nil {
		t.Fatalf("POST /password-reset/complete failed: %v", err)
	}
	defer compResp.Body.Close()

	if compResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on complete, got %d", compResp.StatusCode)
	}

	// 5. Verify sessions were revoked
	var activeSessionsAfter int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&activeSessionsAfter)
	if activeSessionsAfter != 0 {
		t.Errorf("expected 0 active sessions after password reset, got %d", activeSessionsAfter)
	}

	// 6. Verify old password no longer works
	oldLoginResp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		mustMarshal(t, auth.LoginRequest{Email: email, Password: oldPassword}))
	if err != nil {
		t.Fatalf("old login request failed: %v", err)
	}
	oldLoginResp.Body.Close()
	if oldLoginResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for old password, got %d", oldLoginResp.StatusCode)
	}

	// 7. Verify new password logs in successfully
	newLoginResp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		mustMarshal(t, auth.LoginRequest{Email: email, Password: newPassword}))
	if err != nil {
		t.Fatalf("new login request failed: %v", err)
	}
	defer newLoginResp.Body.Close()

	if newLoginResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for new password, got %d", newLoginResp.StatusCode)
	}

	var foundCookie bool
	for _, c := range newLoginResp.Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			foundCookie = true
			break
		}
	}
	if !foundCookie {
		t.Errorf("expected session cookie on login with new password")
	}
}

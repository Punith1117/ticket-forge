package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/jackc/pgx/v5/pgxpool"
)

// createVerifiedUser registers and completes registration for a test user, returning the raw password used.
func createVerifiedUser(t *testing.T, pool *pgxpool.Pool, service *auth.Service, encrypter *security.TokenEncrypter, email, password string) {
	t.Helper()
	ctx := context.Background()

	if err := service.RequestRegistration(ctx, email); err != nil {
		t.Fatalf("createVerifiedUser: RequestRegistration failed: %v", err)
	}

	var payloadJSON []byte
	if err := pool.QueryRow(ctx,
		"SELECT payload FROM outbox_events WHERE payload->>'recipient' = $1 ORDER BY occurred_at DESC LIMIT 1",
		email,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("createVerifiedUser: failed to fetch outbox payload: %v", err)
	}

	payload, err := outbox.ParseEmailPayload(payloadJSON)
	if err != nil {
		t.Fatalf("createVerifiedUser: failed to parse outbox payload: %v", err)
	}

	rawToken, err := encrypter.DecryptToken(payload.EncryptedToken, []byte(security.PurposeRegistrationVerification))
	if err != nil {
		t.Fatalf("createVerifiedUser: failed to decrypt token: %v", err)
	}

	if _, err := service.CompleteRegistration(ctx, rawToken, password, "Test User"); err != nil {
		t.Fatalf("createVerifiedUser: CompleteRegistration failed: %v", err)
	}
}

func TestLogin_Success(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_success@example.com"
	password := "LoginPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, info, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if rawToken == "" {
		t.Error("expected non-empty raw token")
	}
	if info.SessionID == "" {
		t.Error("expected non-empty SessionID")
	}
	if info.UserID == "" {
		t.Error("expected non-empty UserID")
	}
	if info.AbsoluteExpiresAt.Before(time.Now()) {
		t.Error("expected AbsoluteExpiresAt to be in the future")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_wrongpw@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "CorrectPassword1234!")

	_, _, err := service.Login(context.Background(), email, "WrongPassword9999!")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for wrong password, got %v", err)
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	_, _, err := service.Login(context.Background(), "nobody@example.com", "SomePassword1234!")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for unknown email, got %v", err)
	}
}

func TestLogin_EmptyPassword(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_emptypw@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "ValidPassword1234!")

	_, _, err := service.Login(context.Background(), email, "")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for empty password, got %v", err)
	}
}

func TestLogin_InvalidEmail(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	_, _, err := service.Login(context.Background(), "not-an-email", "SomePassword1234!")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for invalid email, got %v", err)
	}
}

func TestLogin_NoSessionOnFailure(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_nosession@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "ValidPassword1234!")

	// Attempt with wrong password
	_, _, _ = service.Login(context.Background(), email, "WrongPassword9999!")

	var sessionCount int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&sessionCount)
	if sessionCount != 0 {
		t.Errorf("expected 0 sessions on failed login, got %d", sessionCount)
	}
}

func TestLogin_ExactlyOneSessionOnSuccess(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_onesession@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	_, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	var sessionCount int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&sessionCount)
	if sessionCount != 1 {
		t.Errorf("expected exactly 1 session after login, got %d", sessionCount)
	}
}

func TestLogin_TokenHashStoredNotRawToken(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_hashcheck@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	var storedHash string
	_ = pool.QueryRow(context.Background(), "SELECT token_hash FROM sessions LIMIT 1").Scan(&storedHash)

	if storedHash == rawToken {
		t.Error("CRITICAL: raw session token is stored in database!")
	}
	if storedHash != security.HashToken(rawToken) {
		t.Error("stored hash does not match expected SHA-256 hash of raw token")
	}
	if len(storedHash) != 64 {
		t.Errorf("expected 64-char hex token hash, got %d chars", len(storedHash))
	}
}

func TestLogin_SessionExpirationPersisted(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_expiry@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	before := time.Now()
	_, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	after := time.Now()

	var idleExpiresAt, absoluteExpiresAt time.Time
	_ = pool.QueryRow(context.Background(),
		"SELECT idle_expires_at, absolute_expires_at FROM sessions LIMIT 1",
	).Scan(&idleExpiresAt, &absoluteExpiresAt)

	expectedIdle := before.Add(auth.SessionIdleTimeout)
	expectedAbs := before.Add(auth.SessionAbsoluteLifetime)
	tolerance := after.Sub(before) + 2*time.Second

	if idleExpiresAt.Before(expectedIdle) || idleExpiresAt.After(expectedIdle.Add(tolerance)) {
		t.Errorf("idle_expires_at %v not within expected range [%v, %v]", idleExpiresAt, expectedIdle, expectedIdle.Add(tolerance))
	}
	if absoluteExpiresAt.Before(expectedAbs) || absoluteExpiresAt.After(expectedAbs.Add(tolerance)) {
		t.Errorf("absolute_expires_at %v not within expected range [%v, %v]", absoluteExpiresAt, expectedAbs, expectedAbs.Add(tolerance))
	}
}

func TestLogin_ConcurrentLoginProducesIndependentSessions(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "login_concurrent@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	const numWorkers = 5
	var wg sync.WaitGroup
	tokens := make([]string, numWorkers)
	errs := make([]error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rawToken, _, err := service.Login(context.Background(), email, password)
			tokens[idx] = rawToken
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d login failed: %v", i, err)
		}
	}

	// All tokens should be unique
	seen := make(map[string]bool)
	for i, tok := range tokens {
		if tok == "" {
			t.Errorf("worker %d got empty token", i)
			continue
		}
		if seen[tok] {
			t.Errorf("duplicate token at worker %d: %s", i, tok)
		}
		seen[tok] = true
	}

	var sessionCount int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&sessionCount)
	if sessionCount != numWorkers {
		t.Errorf("expected %d sessions, got %d", numWorkers, sessionCount)
	}
}

// --- HTTP handler integration tests ---

func TestHTTP_Login_CorrectStatusCodes(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "http_status@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	handler := auth.NewHandler(service, nil, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	tests := []struct {
		name       string
		email      string
		password   string
		wantStatus int
	}{
		{"success", email, password, http.StatusOK},
		{"wrong password", email, "WrongPassword9999!", http.StatusUnauthorized},
		{"unknown email", "nobody@example.com", password, http.StatusUnauthorized},
		{"missing email", "", password, http.StatusBadRequest},
		{"missing password", email, "", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := mustMarshal(t, auth.LoginRequest{Email: tc.email, Password: tc.password})
			resp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json", body)
			if err != nil {
				t.Fatalf("POST /login failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestHTTP_Login_CookiePresentOnSuccess(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "http_cookie@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	handler := auth.NewHandler(service, nil, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json",
		mustMarshal(t, auth.LoginRequest{Email: email, Password: password}))
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q cookie in response, got none", auth.SessionCookieName)
	}
}

func TestHTTP_Login_CookieAttributesOnSuccess(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "http_cookieattr@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	handler := auth.NewHandler(service, nil, false) // secureCookies=false
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json",
		mustMarshal(t, auth.LoginRequest{Email: email, Password: password}))
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	var c *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == auth.SessionCookieName {
			c = cookie
			break
		}
	}
	if c == nil {
		t.Fatal("session_token cookie not found in response")
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly=true")
	}
	if c.Secure {
		t.Error("expected Secure=false (secureCookies=false)")
	}
	if c.Path != "/" {
		t.Errorf("expected Path=/, got %q", c.Path)
	}
	if c.MaxAge != int(auth.SessionAbsoluteLifetime.Seconds()) {
		t.Errorf("expected MaxAge=%d, got %d", int(auth.SessionAbsoluteLifetime.Seconds()), c.MaxAge)
	}
	if c.Value == "" {
		t.Error("expected non-empty cookie value (raw token)")
	}
}

func TestHTTP_Login_NoCookieOnFailure(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "http_nocookie@example.com"
	createVerifiedUser(t, pool, service, encrypter, email, "ValidPassword1234!")

	handler := auth.NewHandler(service, nil, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json",
		mustMarshal(t, auth.LoginRequest{Email: email, Password: "WrongPassword9999!"}))
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Errorf("expected no %q cookie on failure, but found one", auth.SessionCookieName)
		}
	}
}

func TestHTTP_Login_NoTokenInResponseBody(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	email := "http_notokeninbody@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	handler := auth.NewHandler(service, nil, false)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json",
		mustMarshal(t, auth.LoginRequest{Email: email, Password: password}))
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	// Find the cookie value (raw token)
	var rawToken string
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			rawToken = c.Value
		}
	}
	if rawToken == "" {
		t.Fatal("no session cookie found — cannot perform body leak check")
	}

	// Verify the raw token doesn't appear anywhere in the JSON body
	bodyStr, _ := json.Marshal(body)
	if strings.Contains(string(bodyStr), rawToken) {
		t.Error("CRITICAL: raw session token found in HTTP response body!")
	}
}

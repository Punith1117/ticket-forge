package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/security"
)

// loginAndGetSession is a test helper that logs a verified user in and returns the
// raw session token plus the session ID (UUID string) for use in subsequent assertions.
func loginAndGetSession(
	t *testing.T,
	service *auth.Service,
	email, password string,
) (rawToken, sessionID string) {
	t.Helper()
	ctx := context.Background()

	rawToken, _, err := service.Login(ctx, email, password)
	if err != nil {
		t.Fatalf("loginAndGetSession: Login failed: %v", err)
	}

	tokenHash := security.HashToken(rawToken)
	session, err := service.ValidateSession(ctx, tokenHash)
	if err != nil {
		t.Fatalf("loginAndGetSession: ValidateSession failed: %v", err)
	}

	// Extract the session UUID as a hex string directly from the database row.
	// pgtype.UUID stores bytes; encode them as the standard 8-4-4-4-12 format.
	var uuidBytes [16]byte
	copy(uuidBytes[:], session.ID.Bytes[:])
	sessionID = formatUUID(uuidBytes)
	return rawToken, sessionID
}

// formatUUID formats a 16-byte array as a standard UUID string (8-4-4-4-12).
func formatUUID(b [16]byte) string {
	const hex = "0123456789abcdef"
	var buf [36]byte
	for i, j := 0, 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			buf[j] = '-'
			j++
		}
		buf[j] = hex[b[i]>>4]
		buf[j+1] = hex[b[i]&0xf]
		j += 2
	}
	return string(buf[:])
}

// --- Service-level logout tests ---

func TestLogout_RevokesCurrentSession(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "logout.revoke@example.com", "StrongPassw0rd!lo1"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, sessionID := loginAndGetSession(t, service, email, password)
	tokenHash := security.HashToken(rawToken)

	if err := service.Logout(context.Background(), sessionID); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	// Verify revoked_at is now set.
	var revokedAt *string
	if err := pool.QueryRow(context.Background(),
		"SELECT revoked_at::text FROM sessions WHERE token_hash = $1",
		tokenHash,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("SELECT revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Error("revoked_at must be set after Logout")
	}
}

func TestLogout_RevokedSessionCannotAuthenticate(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "logout.auth@example.com", "StrongPassw0rd!lo2"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, sessionID := loginAndGetSession(t, service, email, password)
	tokenHash := security.HashToken(rawToken)

	if err := service.Logout(context.Background(), sessionID); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	_, err := service.ValidateSession(context.Background(), tokenHash)
	if !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound after logout, got %v", err)
	}
}

func TestLogout_DoesNotRevokeOtherSessions(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "logout.other@example.com", "StrongPassw0rd!lo3"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken1, sessionID1 := loginAndGetSession(t, service, email, password)
	rawToken2, _ := loginAndGetSession(t, service, email, password)
	tokenHash2 := security.HashToken(rawToken2)

	// Logout session 1 only.
	if err := service.Logout(context.Background(), sessionID1); err != nil {
		t.Fatalf("Logout session 1: %v", err)
	}

	// Session 2 must still be valid.
	_, err := service.ValidateSession(context.Background(), tokenHash2)
	if err != nil {
		t.Errorf("session 2 must still be valid after session 1 logout, got: %v", err)
	}

	_ = rawToken1 // used only for setup
}

// --- HTTP-level logout tests ---

func newLogoutTestServer(t *testing.T, service *auth.Service) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	handler := auth.NewHandler(service, nil, false)
	handler.RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func TestHTTP_Logout_ClearsCookieAndRevokesSession(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "http.logout@example.com", "StrongPassw0rd!lo4"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _ := loginAndGetSession(t, service, email, password)
	tokenHash := security.HashToken(rawToken)

	ts := newLogoutTestServer(t, service)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Verify the session was revoked in the database.
	var revokedAt *string
	if err := pool.QueryRow(context.Background(),
		"SELECT revoked_at::text FROM sessions WHERE token_hash = $1",
		tokenHash,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("SELECT revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Error("revoked_at must be set after HTTP logout")
	}

	// Verify the Set-Cookie header clears the session cookie.
	var cleared bool
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			if c.MaxAge < 0 {
				cleared = true
			} else {
				t.Errorf("session cookie MaxAge must be negative to clear it, got %d", c.MaxAge)
			}
			if c.Value != "" {
				t.Errorf("session cookie Value must be empty to clear it, got %q", c.Value)
			}
		}
	}
	if !cleared {
		t.Error("response must include a Set-Cookie header clearing the session cookie")
	}
}

func TestHTTP_Logout_WithoutSession_Returns401(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ts := newLogoutTestServer(t, service)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// No cookie — unauthenticated request.

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated logout, got %d", resp.StatusCode)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if errResp.Error.Code != "UNAUTHORIZED" {
		t.Errorf("expected error 'UNAUTHORIZED', got %q", errResp.Error)
	}
}

func TestHTTP_Logout_CookieSecurityAttributes(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "http.cookieattr@example.com", "StrongPassw0rd!lo5"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _ := loginAndGetSession(t, service, email, password)

	ts := newLogoutTestServer(t, service)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	for _, c := range resp.Cookies() {
		if c.Name != auth.SessionCookieName {
			continue
		}
		if !c.HttpOnly {
			t.Error("cleared session cookie must have HttpOnly=true")
		}
		if c.Path != "/" {
			t.Errorf("cleared session cookie Path must be '/', got %q", c.Path)
		}
		if c.MaxAge >= 0 {
			t.Errorf("cleared session cookie MaxAge must be negative, got %d", c.MaxAge)
		}
		// SameSite: httptest server uses http://, so Go's cookie parsing won't expose
		// SameSite to us via resp.Cookies() directly — but we verify it's in the header.
		setCookieHeader := resp.Header.Get("Set-Cookie")
		if !strings.Contains(strings.ToLower(setCookieHeader), "samesite=lax") {
			t.Errorf("cleared session cookie must have SameSite=Lax, header: %q", setCookieHeader)
		}
		return
	}
	t.Error("no Set-Cookie header found for session_token")
}

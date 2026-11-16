package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/security"
)

// --- Unit tests (no database required) ---

func TestMiddleware_NoCookie_Returns401(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMiddleware_EmptyCookieValue_Returns401(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: ""})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for empty cookie, got %d", rec.Code)
	}
}

func TestMiddleware_NextNotCalledOnFailure(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if nextCalled {
		t.Error("next handler must not be called when authentication fails")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestUserFromContext_MissingReturnsNotOk(t *testing.T) {
	_, ok := auth.UserFromContext(context.Background())
	if ok {
		t.Error("UserFromContext must return ok=false for a plain background context")
	}
}

// --- Integration tests (require PostgreSQL) ---

func TestMiddleware_UnknownToken_Returns401(t *testing.T) {
	pool, service, _ := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	handler := auth.NewHandler(service, nil, false)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Generate a valid-looking token that was never stored.
	rawToken, err := security.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unknown token, got %d", rec.Code)
	}
}

func TestMiddleware_ValidSession_AuthenticatesAndPassesThrough(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.valid@example.com", "StrongPassw0rd!123"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)

	var capturedUser auth.AuthenticatedUser
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFromContext(r.Context())
		if !ok {
			t.Error("UserFromContext returned ok=false inside authenticated handler")
		}
		capturedUser = u
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for valid session, got %d", rec.Code)
	}
	if capturedUser.UserID == "" {
		t.Error("UserID must not be empty")
	}
	if capturedUser.SessionID == "" {
		t.Error("SessionID must not be empty")
	}
}

func TestMiddleware_RawTokenNotInContext(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.rawtoken@example.com", "StrongPassw0rd!456"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r.Context())
		if u.UserID == rawToken || u.SessionID == rawToken {
			t.Error("raw session token must not be stored in the context")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)
}

func TestMiddleware_RevokedSession_Returns401(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.revoked@example.com", "StrongPassw0rd!789"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Revoke the session by using the middleware itself to get the session ID
	// from context, then calling Logout with it.
	tokenHash := security.HashToken(rawToken)

	// Expire the session directly via SQL to avoid a dependency on UUIDString.
	_, err = pool.Exec(context.Background(),
		"UPDATE sessions SET revoked_at = now() WHERE token_hash = $1",
		tokenHash,
	)
	if err != nil {
		t.Fatalf("UPDATE revoked_at: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for revoked session, got %d", rec.Code)
	}
}

func TestMiddleware_IdleExpiredSession_Returns401(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.idleexp@example.com", "StrongPassw0rd!abc"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	tokenHash := security.HashToken(rawToken)

	// Expire idle timeout in the past.
	_, err = pool.Exec(context.Background(),
		"UPDATE sessions SET idle_expires_at = now() - interval '1 second' WHERE token_hash = $1",
		tokenHash,
	)
	if err != nil {
		t.Fatalf("UPDATE idle_expires_at: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for idle-expired session, got %d", rec.Code)
	}
}

func TestMiddleware_AbsoluteExpiredSession_Returns401(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.absexp@example.com", "StrongPassw0rd!def"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	tokenHash := security.HashToken(rawToken)

	// Expire both timeouts in the past.
	_, err = pool.Exec(context.Background(),
		`UPDATE sessions
		    SET idle_expires_at     = now() - interval '1 hour',
		        absolute_expires_at = now() - interval '1 hour'
		  WHERE token_hash = $1`,
		tokenHash,
	)
	if err != nil {
		t.Fatalf("UPDATE absolute_expires_at: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for absolutely-expired session, got %d", rec.Code)
	}
}

func TestMiddleware_IdleRefresh_UpdatesIdleExpiry(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.refresh@example.com", "StrongPassw0rd!ghi"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	tokenHash := security.HashToken(rawToken)

	// Record the initial idle expiry.
	var initialIdleExpiry time.Time
	if err := pool.QueryRow(context.Background(),
		"SELECT idle_expires_at FROM sessions WHERE token_hash = $1",
		tokenHash,
	).Scan(&initialIdleExpiry); err != nil {
		t.Fatalf("SELECT idle_expires_at: %v", err)
	}

	// Set idle_expires_at to a small window so a refresh would be detectable.
	_, err = pool.Exec(context.Background(),
		"UPDATE sessions SET idle_expires_at = now() + interval '5 minutes' WHERE token_hash = $1",
		tokenHash,
	)
	if err != nil {
		t.Fatalf("UPDATE idle_expires_at: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Idle expiry should have been pushed forward (back to ~30 minutes from now).
	var updatedIdleExpiry time.Time
	if err := pool.QueryRow(context.Background(),
		"SELECT idle_expires_at FROM sessions WHERE token_hash = $1",
		tokenHash,
	).Scan(&updatedIdleExpiry); err != nil {
		t.Fatalf("SELECT idle_expires_at after request: %v", err)
	}

	// The updated idle expiry must be later than 5 minutes from now (our artificial value).
	if !updatedIdleExpiry.After(time.Now().Add(5 * time.Minute)) {
		t.Errorf("idle expiry was not refreshed: got %v, want > %v",
			updatedIdleExpiry, time.Now().Add(5*time.Minute))
	}
}

func TestMiddleware_IdleRefresh_NeverExceedsAbsoluteExpiry(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	const email, password = "mw.clamp@example.com", "StrongPassw0rd!jkl"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	rawToken, _, err := service.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	tokenHash := security.HashToken(rawToken)

	// Set absolute expiry to only 5 minutes away; idle expiry must be clamped to it.
	_, err = pool.Exec(context.Background(),
		`UPDATE sessions
		    SET absolute_expires_at = now() + interval '5 minutes',
		        idle_expires_at     = now() + interval '5 minutes'
		  WHERE token_hash = $1`,
		tokenHash,
	)
	if err != nil {
		t.Fatalf("UPDATE expiry: %v", err)
	}

	handler := auth.NewHandler(service, nil, false)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: rawToken})
	rec := httptest.NewRecorder()

	handler.Middleware(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var idleExpiry, absoluteExpiry time.Time
	if err := pool.QueryRow(context.Background(),
		"SELECT idle_expires_at, absolute_expires_at FROM sessions WHERE token_hash = $1",
		tokenHash,
	).Scan(&idleExpiry, &absoluteExpiry); err != nil {
		t.Fatalf("SELECT expiry: %v", err)
	}

	if idleExpiry.After(absoluteExpiry) {
		t.Errorf("idle_expires_at (%v) must not exceed absolute_expires_at (%v)", idleExpiry, absoluteExpiry)
	}
}

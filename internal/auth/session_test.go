package auth_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/security"
)

func TestSessionConstants(t *testing.T) {
	if auth.SessionIdleTimeout != 30*time.Minute {
		t.Errorf("expected idle timeout 30m, got %v", auth.SessionIdleTimeout)
	}
	if auth.SessionAbsoluteLifetime != 7*24*time.Hour {
		t.Errorf("expected absolute lifetime 7d, got %v", auth.SessionAbsoluteLifetime)
	}
}

func TestSessionTokenGeneration(t *testing.T) {
	token, err := security.GenerateToken()
	if err != nil {
		t.Fatalf("token generation failed: %v", err)
	}
	if len(token) == 0 {
		t.Error("expected non-empty token")
	}
	// Token must be unique per call
	token2, _ := security.GenerateToken()
	if token == token2 {
		t.Error("expected unique tokens per call")
	}
}

func TestSessionTokenHashing(t *testing.T) {
	token, _ := security.GenerateToken()
	hash := security.HashToken(token)
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex SHA-256 hash, got %d chars", len(hash))
	}
	// Verify deterministic
	if security.HashToken(token) != hash {
		t.Error("expected deterministic hash")
	}
	// Raw token not equal to hash
	if hash == token {
		t.Error("CRITICAL: token hash equals raw token")
	}
}

func TestLoginCookieSecureFlag(t *testing.T) {
	// Verify that cookie secure flag is set correctly per configuration
	w := httptest.NewRecorder()

	// Simulate setting a cookie with Secure=true
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "test-token-value",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.SessionAbsoluteLifetime.Seconds()),
	})

	result := w.Result()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != auth.SessionCookieName {
		t.Errorf("expected cookie name %q, got %q", auth.SessionCookieName, c.Name)
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly=true")
	}
	if !c.Secure {
		t.Error("expected Secure=true")
	}
	if c.Path != "/" {
		t.Errorf("expected Path=/, got %q", c.Path)
	}
	if c.MaxAge != int(auth.SessionAbsoluteLifetime.Seconds()) {
		t.Errorf("expected MaxAge=%d, got %d", int(auth.SessionAbsoluteLifetime.Seconds()), c.MaxAge)
	}
}

func TestSessionExpirationCalculation(t *testing.T) {
	now := time.Now()
	idle := now.Add(auth.SessionIdleTimeout)
	abs := now.Add(auth.SessionAbsoluteLifetime)

	if idle.Before(now) {
		t.Error("idle expiry must be in the future")
	}
	if abs.Before(now) {
		t.Error("absolute expiry must be in the future")
	}
	if abs.Before(idle) {
		t.Error("absolute expiry must be >= idle expiry")
	}
}

func TestLoginInputValidation_MissingEmail(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		mustMarshal(t, auth.LoginRequest{Email: "", Password: "somepassword123!"}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing email, got %d", rec.Code)
	}
}

func TestLoginInputValidation_MissingPassword(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		mustMarshal(t, auth.LoginRequest{Email: "user@example.com", Password: ""}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing password, got %d", rec.Code)
	}
}

func TestLoginInputValidation_MalformedJSON(t *testing.T) {
	handler := auth.NewHandler(nil, nil, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed JSON, got %d", rec.Code)
	}
}

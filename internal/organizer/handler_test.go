package organizer

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	config.Load("../../.env")

	cfg := database.ConfigFromEnv()

	testDB := os.Getenv("TEST_POSTGRES_DB")
	if testDB == "" {
		t.Fatal("TEST_POSTGRES_DB must be set")
	}
	cfg.Database = testDB

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

func setupAuthenticatedRequest(t *testing.T, pool *pgxpool.Pool) (*http.ServeMux, *http.Request, string) {
	t.Helper()

	ctx := context.Background()

	// Create an isolated user.
	email := "organizer_test_" + time.Now().Format("20060102150405.000000000") + "@ticketforge.dev"

	var userID string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'test-hash', 'Test Organizer')
		RETURNING id::text
	`, email).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	// Generate a real session token.
	rawToken, err := security.GenerateToken()
	if err != nil {
		t.Fatalf("failed to generate session token: %v", err)
	}

	tokenHash := security.HashToken(rawToken)

	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, now() + interval '30 minutes', now() + interval '7 days')
	`, userID, tokenHash)
	if err != nil {
		t.Fatalf("failed to create test session: %v", err)
	}

	// Clean up only this test's records.
	t.Cleanup(func() {
		ctx := context.Background()

		_, err := pool.Exec(ctx,
			`DELETE FROM sessions WHERE user_id = $1`, userID)
		if err != nil {
			t.Errorf("failed to clean up sessions: %v", err)
		}

		_, err = pool.Exec(ctx,
			`DELETE FROM organizer_profiles WHERE user_id = $1`, userID)
		if err != nil {
			t.Errorf("failed to clean up organizer profile: %v", err)
		}

		_, err = pool.Exec(ctx,
			`DELETE FROM users WHERE id = $1`, userID)
		if err != nil {
			t.Errorf("failed to clean up user: %v", err)
		}
	})

	// Construct the real authentication service and handler.
	keyRing, err := security.NewKeyRing(1, map[int][]byte{
		1: make([]byte, security.AES256KeySize),
	})
	if err != nil {
		t.Fatal(err)
	}

	encrypter, err := security.NewTokenEncrypter(keyRing)
	if err != nil {
		t.Fatal(err)
	}

	authService, err := auth.NewService(pool, encrypter)
	if err != nil {
		t.Fatal(err)
	}

	authHandler := auth.NewHandler(authService, slog.Default(), false)

	organizerService, err := NewService(pool)
	if err != nil {
		t.Fatal(err)
	}

	organizerHandler := NewHandler(organizerService, slog.Default())

	mux := http.NewServeMux()
	organizerHandler.RegisterRoutes(mux, authHandler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/organizer-profiles",
		nil,
	)
	req.AddCookie(&http.Cookie{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	})

	return mux, req, userID
}

func TestCreateProfile(t *testing.T) {
	pool := setupTestDB(t)

	t.Run("unauthenticated", func(t *testing.T) {
		handler := NewHandler(nil, slog.Default())

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/organizer-profiles",
			nil,
		)
		rec := httptest.NewRecorder()

		handler.CreateProfile(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
		}
	})

	t.Run("creates profile and returns existing profile on repeat", func(t *testing.T) {
		mux, req, userID := setupAuthenticatedRequest(t, pool)

		// First request creates the profile.
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status %d, got %d: %s",
				http.StatusCreated, rec.Code, rec.Body.String())
		}

		// Second request must return the existing profile.
		req2 := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/organizer-profiles",
			nil,
		)
		req2.AddCookie(req.Cookies()[0])

		rec2 := httptest.NewRecorder()
		mux.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s",
				http.StatusOK, rec2.Code, rec2.Body.String())
		}

		// Verify that only one profile exists.
		var count int
		err := pool.QueryRow(context.Background(), `
			SELECT count(*)
			FROM organizer_profiles
			WHERE user_id = $1
		`, userID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to count organizer profiles: %v", err)
		}

		if count != 1 {
			t.Errorf("expected exactly 1 organizer profile, got %d", count)
		}
	})
}

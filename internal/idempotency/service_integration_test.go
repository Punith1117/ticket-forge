package idempotency_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupIntegrationDB(t *testing.T) (*pgxpool.Pool, *idempotency.Service) {
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

	return pool, idempotency.NewService()
}

func createTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()

	email := fmt.Sprintf("idempotency_%s@example.com", uuid.NewString())

	var userID string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, 'hash_placeholder', 'Idempotency Test User')
		RETURNING id::text
	`, email).Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return userID
}

func newRequest(userID, operation string) idempotency.Request {
	return idempotency.Request{
		UserID:    userID,
		Operation: operation,
		Key:       uuid.NewString(),
		Payload:   []byte(`{"title":"Test Event"}`),
	}
}

func beginTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	return tx
}

func TestBegin_ClaimsNewKey(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, pool)
	req := newRequest(userID, "event:create")

	tx := beginTx(t, ctx, pool)
	defer tx.Rollback(ctx)

	execution, err := service.Begin(ctx, tx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	if !execution.RecordID.Valid {
		t.Fatal("expected a valid idempotency record ID")
	}

	if execution.Replay != nil {
		t.Fatal("new request should not return a replay")
	}
}

func TestBegin_ReplaysSuccessfulRequest(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, pool)
	req := newRequest(userID, "event:create")

	responseBody := []byte(`{"id":"event-123","title":"Test Event"}`)

	// First request claims and completes the key.
	tx1 := beginTx(t, ctx, pool)

	execution, err := service.Begin(ctx, tx1, req)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}

	err = service.Complete(ctx, tx1, execution, 201, responseBody)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("failed to commit first transaction: %v", err)
	}

	// Retry with the same key and payload.
	tx2 := beginTx(t, ctx, pool)
	defer tx2.Rollback(ctx)

	replay, err := service.Begin(ctx, tx2, req)
	if err != nil {
		t.Fatalf("second Begin failed: %v", err)
	}

	if replay.Replay == nil {
		t.Fatal("expected the original response to be replayed")
	}

	if replay.Replay.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", replay.Replay.StatusCode)
	}

	assertJSONEqual(t, responseBody, replay.Replay.Body)
}

func assertJSONEqual(t *testing.T, expected, actual []byte) {
	t.Helper()

	var expectedJSON any
	if err := json.Unmarshal(expected, &expectedJSON); err != nil {
		t.Fatalf("invalid expected JSON: %v", err)
	}

	var actualJSON any
	if err := json.Unmarshal(actual, &actualJSON); err != nil {
		t.Fatalf("invalid actual JSON: %v", err)
	}

	if !reflect.DeepEqual(expectedJSON, actualJSON) {
		t.Errorf("JSON mismatch:\nexpected: %s\nactual:   %s", expected, actual)
	}
}

func TestBegin_RejectsDifferentPayload(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, pool)
	req := newRequest(userID, "event:create")

	// First request claims and completes the key.
	tx1 := beginTx(t, ctx, pool)

	execution, err := service.Begin(ctx, tx1, req)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}

	err = service.Complete(ctx, tx1, execution, 201, []byte(`{"id":"event-123"}`))
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("failed to commit first transaction: %v", err)
	}

	// Reuse the key with a different payload.
	req.Payload = []byte(`{"title":"Different Event"}`)

	tx2 := beginTx(t, ctx, pool)
	defer tx2.Rollback(ctx)

	_, err = service.Begin(ctx, tx2, req)

	if !errors.Is(err, idempotency.ErrKeyReused) {
		t.Fatalf("expected ErrKeyReused, got %v", err)
	}
}

func TestBegin_ScopesKeysByUserAndOperation(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	user1 := createTestUser(t, ctx, pool)
	user2 := createTestUser(t, ctx, pool)

	key := uuid.NewString()

	req1 := idempotency.Request{
		UserID:    user1,
		Operation: "event:create",
		Key:       key,
		Payload:   []byte(`{"title":"Test Event"}`),
	}

	// Claim and complete the first user's event:create key.
	tx1 := beginTx(t, ctx, pool)

	execution, err := service.Begin(ctx, tx1, req1)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}

	if err := service.Complete(ctx, tx1, execution, 201, []byte(`{"id":"event-123"}`)); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("failed to commit first transaction: %v", err)
	}

	tests := []struct {
		name string
		req  idempotency.Request
	}{
		{
			name: "different user",
			req: idempotency.Request{
				UserID:    user2,
				Operation: "event:create",
				Key:       key,
				Payload:   req1.Payload,
			},
		},
		{
			name: "different operation",
			req: idempotency.Request{
				UserID:    user1,
				Operation: "venue-booking:create",
				Key:       key,
				Payload:   req1.Payload,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := beginTx(t, ctx, pool)
			defer tx.Rollback(ctx)

			result, err := service.Begin(ctx, tx, tt.req)
			if err != nil {
				t.Fatalf("Begin failed: %v", err)
			}

			if !result.RecordID.Valid {
				t.Fatal("expected a new idempotency record")
			}

			if result.Replay != nil {
				t.Fatal("expected a new claim, got a replay")
			}
		})
	}
}

func TestBegin_RollbackReleasesKey(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, pool)
	req := newRequest(userID, "event:create")

	// Claim the key, then roll back.
	tx1 := beginTx(t, ctx, pool)

	_, err := service.Begin(ctx, tx1, req)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}

	if err := tx1.Rollback(ctx); err != nil {
		t.Fatalf("failed to roll back first transaction: %v", err)
	}

	// The same key should now be claimable.
	tx2 := beginTx(t, ctx, pool)
	defer tx2.Rollback(ctx)

	execution, err := service.Begin(ctx, tx2, req)
	if err != nil {
		t.Fatalf("second Begin failed: %v", err)
	}

	if !execution.RecordID.Valid {
		t.Fatal("expected a new idempotency record after rollback")
	}

	if execution.Replay != nil {
		t.Fatal("expected a new claim, got a replay")
	}
}

func TestBegin_ConcurrentRequests(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID := createTestUser(t, ctx, pool)
	req := newRequest(userID, "event:create")

	// Request A claims the key.
	tx1 := beginTx(t, ctx, pool)

	execution, err := service.Begin(ctx, tx1, req)
	if err != nil {
		t.Fatalf("first Begin failed: %v", err)
	}

	responseBody := []byte(`{"id":"event-123"}`)

	if err := service.Complete(ctx, tx1, execution, 201, responseBody); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Request B attempts to claim the same key while A's
	// transaction is still open.
	tx2 := beginTx(t, ctx, pool)
	defer tx2.Rollback(ctx)

	started := make(chan struct{})
	resultCh := make(chan idempotency.Execution, 1)
	errCh := make(chan error, 1)

	go func() {
		close(started)

		result, err := service.Begin(ctx, tx2, req)
		if err != nil {
			errCh <- err
			return
		}

		resultCh <- result
	}()

	<-started

	// A commits. B should now observe the completed record.
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("failed to commit first transaction: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("concurrent Begin failed: %v", err)

	case result := <-resultCh:
		if result.Replay == nil {
			t.Fatal("expected concurrent request to replay the original response")
		}

		if result.Replay.StatusCode != 201 {
			t.Errorf("expected replay status 201, got %d", result.Replay.StatusCode)
		}

		assertJSONEqual(t, responseBody, result.Replay.Body)

	case <-ctx.Done():
		t.Fatal("timed out waiting for concurrent request")
	}
}

func TestComplete_RejectsSecondCompletion(t *testing.T) {
	pool, service := setupIntegrationDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, pool)
	req := newRequest(userID, "event:create")

	tx := beginTx(t, ctx, pool)
	defer tx.Rollback(ctx)

	execution, err := service.Begin(ctx, tx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	body := []byte(`{"id":"event-123"}`)

	if err := service.Complete(ctx, tx, execution, 201, body); err != nil {
		t.Fatalf("first Complete failed: %v", err)
	}

	err = service.Complete(ctx, tx, execution, 201, body)
	if !errors.Is(err, idempotency.ErrAlreadyCompleted) {
		t.Fatalf("expected ErrAlreadyCompleted, got %v", err)
	}
}

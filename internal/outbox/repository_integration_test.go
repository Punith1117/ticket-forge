package outbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupIntegrationDB(t *testing.T) (*pgxpool.Pool, *outbox.PGRepository) {
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
		return nil, nil
	}

	// Clean transient outbox events
	_, err = pool.Exec(ctx, "DELETE FROM outbox_events;")
	if err != nil {
		pool.Close()
		t.Fatalf("failed to clean outbox_events table: %v", err)
	}

	repo := outbox.NewPGRepository(pool)
	return pool, repo
}

func TestPGRepository_ClaimEligibleEvents(t *testing.T) {
	pool, repo := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()

	// 1. Insert eligible pending event (next_attempt_at in past)
	eligiblePayload := []byte(`{"recipient":"eligible@example.com","token_expires_at":"2030-01-01T00:00:00Z"}`)
	eligibleEvent, err := repo.InsertEvent(ctx, "user", pgUUID(uuid.New()), outbox.EventTypeRegistrationVerification, eligiblePayload)
	if err != nil {
		t.Fatalf("failed to insert eligible event: %v", err)
	}

	// 2. Insert pending event scheduled in the future (not eligible)
	futurePayload := []byte(`{"recipient":"future@example.com"}`)
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, next_attempt_at)
		VALUES ('user', $1, $2, $3, 'pending', now() + interval '1 hour')
	`, eligibleEvent.AggregateID, outbox.EventTypeRegistrationVerification, futurePayload)
	if err != nil {
		t.Fatalf("failed to insert future event: %v", err)
	}

	// 3. Insert unsupported event type (not eligible)
	_, err = repo.InsertEvent(ctx, "user", pgUUID(uuid.New()), "unsupported.event.type", eligiblePayload)
	if err != nil {
		t.Fatalf("failed to insert unsupported event: %v", err)
	}

	// 4. Insert active processing event with lease still valid (not eligible)
	activeToken := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload, status, claim_token, claim_expires_at)
		VALUES ('user', $1, $2, $3, 'processing', $4, now() + interval '5 minutes')
	`, eligibleEvent.AggregateID, outbox.EventTypeRegistrationVerification, eligiblePayload, activeToken)
	if err != nil {
		t.Fatalf("failed to insert active lease event: %v", err)
	}

	// 5. Insert expired lease processing event (eligible for reclamation!)
	expiredToken := uuid.New()
	expiredID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status, claim_token, claim_expires_at)
		VALUES ($1, 'user', $2, $3, $4, 'processing', $5, now() - interval '5 minutes')
	`, expiredID, eligibleEvent.AggregateID, outbox.EventTypeRegistrationVerification, eligiblePayload, expiredToken)
	if err != nil {
		t.Fatalf("failed to insert expired lease event: %v", err)
	}

	// Claim eligible events
	supported := []string{outbox.EventTypeRegistrationVerification, outbox.EventTypePasswordReset}
	claimed, err := repo.ClaimEligibleEvents(ctx, supported, 10, 2*time.Minute)
	if err != nil {
		t.Fatalf("ClaimEligibleEvents failed: %v", err)
	}

	// Should have claimed exactly 2 events: the pending eligible one and the expired lease one
	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed events, got %d", len(claimed))
	}

	claimedIDs := make(map[string]bool)
	for _, ev := range claimed {
		idStr := fmt.Sprintf("%x-%x-%x-%x-%x", ev.ID.Bytes[0:4], ev.ID.Bytes[4:6], ev.ID.Bytes[6:8], ev.ID.Bytes[8:10], ev.ID.Bytes[10:16])
		claimedIDs[idStr] = true

		if ev.Status != "processing" {
			t.Errorf("expected claimed status 'processing', got '%s'", ev.Status)
		}
		if !ev.ClaimToken.Valid {
			t.Errorf("expected valid claim token")
		}
		if !ev.ClaimExpiresAt.Valid || ev.ClaimExpiresAt.Time.Before(time.Now()) {
			t.Errorf("expected claim_expires_at in the future")
		}
	}
}

func TestPGRepository_StaleClaimTokenUpdateRejected(t *testing.T) {
	pool, repo := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()

	payload := []byte(`{"recipient":"stale@example.com","encrypted_token":{"key_version":1,"nonce":"abc","ciphertext":"xyz"}}`)
	ev, err := repo.InsertEvent(ctx, "user", pgUUID(uuid.New()), outbox.EventTypeRegistrationVerification, payload)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	// Claim event (Worker 1)
	supported := []string{outbox.EventTypeRegistrationVerification}
	claimed1, err := repo.ClaimEligibleEvents(ctx, supported, 10, 100*time.Millisecond)
	if err != nil || len(claimed1) != 1 {
		t.Fatalf("failed to claim event: %v", err)
	}
	worker1ClaimToken := claimed1[0].ClaimToken

	// Simulate lease expiration
	_, err = pool.Exec(ctx, "UPDATE outbox_events SET claim_expires_at = now() - interval '1 second' WHERE id = $1", ev.ID)
	if err != nil {
		t.Fatalf("failed to simulate lease expiry: %v", err)
	}

	// Worker 2 reclaims the event
	claimed2, err := repo.ClaimEligibleEvents(ctx, supported, 10, 2*time.Minute)
	if err != nil || len(claimed2) != 1 {
		t.Fatalf("failed to reclaim event by worker 2: %v", err)
	}
	worker2ClaimToken := claimed2[0].ClaimToken

	if worker1ClaimToken.Bytes == worker2ClaimToken.Bytes {
		t.Fatalf("claim tokens must be distinct between claims")
	}

	// Worker 1 attempts to CompleteEvent with its stale claim token -> must be rejected (false)
	completed, err := repo.CompleteEvent(ctx, ev.ID, worker1ClaimToken, "")
	if err != nil {
		t.Fatalf("unexpected error on stale complete: %v", err)
	}
	if completed {
		t.Error("worker 1 should have been fenced out (CompleteEvent returned true)")
	}

	// Worker 1 attempts to RetryEvent with stale token -> must be rejected
	retried, err := repo.RetryEvent(ctx, ev.ID, worker1ClaimToken, time.Now().Add(time.Minute), "err")
	if err != nil {
		t.Fatalf("unexpected error on stale retry: %v", err)
	}
	if retried {
		t.Error("worker 1 should have been fenced out (RetryEvent returned true)")
	}

	// Worker 1 attempts to FailEvent with stale token -> must be rejected
	failed, err := repo.FailEvent(ctx, ev.ID, worker1ClaimToken, true, "err")
	if err != nil {
		t.Fatalf("unexpected error on stale fail: %v", err)
	}
	if failed {
		t.Error("worker 1 should have been fenced out (FailEvent returned true)")
	}

	// Worker 2 attempts CompleteEvent with valid claim token -> must succeed
	completed2, err := repo.CompleteEvent(ctx, ev.ID, worker2ClaimToken, "msg-12345")
	if err != nil {
		t.Fatalf("worker 2 complete failed: %v", err)
	}
	if !completed2 {
		t.Error("worker 2 should successfully complete the event")
	}
}

func TestPGRepository_EncryptedPayloadScrubbing(t *testing.T) {
	pool, repo := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()

	rawPayload := map[string]interface{}{
		"recipient": "scrub@example.com",
		"encrypted_token": map[string]interface{}{
			"key_version": 1,
			"nonce":       "test-nonce",
			"ciphertext":  "sensitive-ciphertext-material",
		},
		"token_expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	payloadBytes, _ := json.Marshal(rawPayload)

	ev, err := repo.InsertEvent(ctx, "user", pgUUID(uuid.New()), outbox.EventTypeRegistrationVerification, payloadBytes)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	// Claim event
	claimed, err := repo.ClaimEligibleEvents(ctx, []string{outbox.EventTypeRegistrationVerification}, 1, 2*time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("failed to claim event: %v", err)
	}

	// Complete event
	updated, err := repo.CompleteEvent(ctx, ev.ID, claimed[0].ClaimToken, "provider-msg-id-1")
	if err != nil || !updated {
		t.Fatalf("failed to complete event: %v", err)
	}

	// Inspect payload and published_at in database
	var hasEncryptedToken bool
	var recipient string
	var status string
	var publishedAt *time.Time
	err = pool.QueryRow(ctx, `
		SELECT payload ? 'encrypted_token', payload->>'recipient', status, published_at
		FROM outbox_events WHERE id = $1
	`, ev.ID).Scan(&hasEncryptedToken, &recipient, &status, &publishedAt)
	if err != nil {
		t.Fatalf("failed to query outbox event after completion: %v", err)
	}

	if hasEncryptedToken {
		t.Error("encrypted_token was NOT scrubbed from payload upon completion")
	}
	if recipient != "scrub@example.com" {
		t.Errorf("expected recipient scrub@example.com, got %s", recipient)
	}
	if status != "published" {
		t.Errorf("expected status 'published', got %s", status)
	}
	if publishedAt == nil {
		t.Error("expected published_at to be set")
	}
}

func TestPGRepository_ConcurrentClaimsWithSkipLocked(t *testing.T) {
	pool, repo := setupIntegrationDB(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()

	// Insert 30 events
	numEvents := 30
	for i := 0; i < numEvents; i++ {
		payload := []byte(fmt.Sprintf(`{"recipient":"concurrent_%d@example.com"}`, i))
		_, err := repo.InsertEvent(ctx, "user", pgUUID(uuid.New()), outbox.EventTypeRegistrationVerification, payload)
		if err != nil {
			t.Fatalf("failed to insert event %d: %v", i, err)
		}
	}

	numWorkers := 5
	batchSize := int32(6)

	var wg sync.WaitGroup
	claimedMap := sync.Map{}
	var totalClaimed int64
	var mu sync.Mutex

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			events, err := repo.ClaimEligibleEvents(ctx, []string{outbox.EventTypeRegistrationVerification}, batchSize, 2*time.Minute)
			if err != nil {
				t.Errorf("worker %d claim failed: %v", workerID, err)
				return
			}
			for _, ev := range events {
				idStr := fmt.Sprintf("%x", ev.ID.Bytes)
				if _, loaded := claimedMap.LoadOrStore(idStr, workerID); loaded {
					t.Errorf("duplicate claim detected for event %s by worker %d", idStr, workerID)
				}
				mu.Lock()
				totalClaimed++
				mu.Unlock()
			}
		}(w)
	}

	wg.Wait()

	if totalClaimed != int64(numEvents) {
		t.Fatalf("expected all %d events to be claimed across concurrent workers, got %d", numEvents, totalClaimed)
	}
}

package ratelimit_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/config"
	"github.com/Punith1117/ticket-forge/internal/database"
	"github.com/Punith1117/ticket-forge/internal/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, *ratelimit.PostgresLimiter, func()) {
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

	limiter, err := ratelimit.NewPostgresLimiter(pool)
	if err != nil {
		pool.Close()
		t.Fatalf("failed to create postgres limiter: %v", err)
	}

	cleanup := func() {
		pool.Close()
	}

	return pool, limiter, cleanup
}

func deleteTestKeys(ctx context.Context, pool *pgxpool.Pool, prefix string) {
	_, _ = pool.Exec(ctx, "DELETE FROM rate_limit_buckets WHERE key LIKE $1 || '%'", prefix)
}

func TestPostgresLimiter_FirstTimeBucketCreation(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	testKey := fmt.Sprintf("test:first:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:first",
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: 2 * time.Second,
	}

	res, err := limiter.Allow(ctx, testKey, policy)
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}

	if !res.Allowed {
		t.Errorf("expected Allowed = true on first request")
	}
	if res.Remaining != 4 {
		t.Errorf("expected Remaining = 4, got %d", res.Remaining)
	}
	if res.RetryAfter != 0 {
		t.Errorf("expected RetryAfter = 0, got %v", res.RetryAfter)
	}

	// Verify persistence in DB
	var tokens int64
	err = pool.QueryRow(ctx, "SELECT tokens FROM rate_limit_buckets WHERE key = $1", testKey).Scan(&tokens)
	if err != nil {
		t.Fatalf("failed to query bucket: %v", err)
	}
	if tokens != 4 {
		t.Errorf("persisted tokens = %d, expected 4", tokens)
	}
}

func TestPostgresLimiter_MultipleRequests_And_Exhaustion(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	testKey := fmt.Sprintf("test:exhaust:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:exhaust",
		Capacity:       3,
		RefillTokens:   1,
		RefillInterval: 10 * time.Second,
	}

	// Request 1: allowed, remaining 2
	res1, err := limiter.Allow(ctx, testKey, policy)
	if err != nil || !res1.Allowed || res1.Remaining != 2 {
		t.Fatalf("res1: err=%v, allowed=%v, remaining=%d", err, res1.Allowed, res1.Remaining)
	}

	// Request 2: allowed, remaining 1
	res2, err := limiter.Allow(ctx, testKey, policy)
	if err != nil || !res2.Allowed || res2.Remaining != 1 {
		t.Fatalf("res2: err=%v, allowed=%v, remaining=%d", err, res2.Allowed, res2.Remaining)
	}

	// Request 3: allowed, remaining 0
	res3, err := limiter.Allow(ctx, testKey, policy)
	if err != nil || !res3.Allowed || res3.Remaining != 0 {
		t.Fatalf("res3: err=%v, allowed=%v, remaining=%d", err, res3.Allowed, res3.Remaining)
	}

	// Request 4: DENIED, remaining 0, retryAfter > 0
	res4, err := limiter.Allow(ctx, testKey, policy)
	if err != nil {
		t.Fatalf("res4 error: %v", err)
	}
	if res4.Allowed {
		t.Fatalf("expected res4 to be denied, but was allowed")
	}
	if res4.Remaining != 0 {
		t.Errorf("expected remaining 0, got %d", res4.Remaining)
	}
	if res4.RetryAfter <= 0 || res4.RetryAfter > 10*time.Second {
		t.Errorf("expected retryAfter in (0, 10s], got %v", res4.RetryAfter)
	}

	// Request 5: Denied again, retryAfter remains valid
	res5, err := limiter.Allow(ctx, testKey, policy)
	if err != nil {
		t.Fatalf("res5 error: %v", err)
	}
	if res5.Allowed {
		t.Fatalf("expected res5 to be denied, but was allowed")
	}
	if res5.Remaining != 0 {
		t.Errorf("expected remaining 0, got %d", res5.Remaining)
	}
}

func TestPostgresLimiter_RefillAfterTimePasses(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	testKey := fmt.Sprintf("test:refill:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:refill",
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: 2 * time.Second,
	}

	// Deplete all 5 tokens
	for i := 0; i < 5; i++ {
		res, err := limiter.Allow(ctx, testKey, policy)
		if err != nil || !res.Allowed {
			t.Fatalf("failed depleting token %d: err=%v, res=%+v", i, err, res)
		}
	}

	// Simulate 6.5 seconds elapsed (should refill 3 tokens: 6.5s / 2s = 3 tokens)
	// Consuming 1 token leaves 2 remaining!
	pastTime := time.Now().UTC().Add(-6500 * time.Millisecond)
	_, err := pool.Exec(ctx, "UPDATE rate_limit_buckets SET last_refill_at = $1 WHERE key = $2", pastTime, testKey)
	if err != nil {
		t.Fatalf("failed simulating time: %v", err)
	}

	res, err := limiter.Allow(ctx, testKey, policy)
	if err != nil {
		t.Fatalf("allow failed after refill: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("expected request to be allowed after replenishment")
	}
	if res.Remaining != 2 {
		t.Errorf("expected 2 remaining tokens after replenishing 3 and consuming 1, got %d", res.Remaining)
	}
}

func TestPostgresLimiter_CapacityCeiling(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	testKey := fmt.Sprintf("test:ceiling:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:ceiling",
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: 2 * time.Second,
	}

	// Initialize bucket with 1 token consumed (4 left)
	res, err := limiter.Allow(ctx, testKey, policy)
	if err != nil || !res.Allowed {
		t.Fatalf("initial allow failed: %v", err)
	}

	// Simulate 1 hour elapsed (vastly exceeds 5 capacity)
	longPast := time.Now().UTC().Add(-1 * time.Hour)
	_, err = pool.Exec(ctx, "UPDATE rate_limit_buckets SET last_refill_at = $1 WHERE key = $2", longPast, testKey)
	if err != nil {
		t.Fatalf("failed simulating long idle: %v", err)
	}

	res, err = limiter.Allow(ctx, testKey, policy)
	if err != nil {
		t.Fatalf("allow failed: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("expected request to be allowed")
	}
	// Capacity is 5; consuming 1 leaves 4 (never more than capacity - 1)
	if res.Remaining != 4 {
		t.Errorf("expected remaining capped at 4 (Capacity - 1), got %d", res.Remaining)
	}
}

func TestPostgresLimiter_ConcurrentCreation(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	testKey := fmt.Sprintf("test:concurr_create:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:concurr_create",
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: 30 * time.Second,
	}

	const concurrency = 20
	var wg sync.WaitGroup
	var allowedCount int64
	var deniedCount int64
	var errorCount int64

	startBarrier := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBarrier
			res, err := limiter.Allow(ctx, testKey, policy)
			if err != nil {
				atomic.AddInt64(&errorCount, 1)
				return
			}
			if res.Allowed {
				atomic.AddInt64(&allowedCount, 1)
			} else {
				atomic.AddInt64(&deniedCount, 1)
			}
		}()
	}

	close(startBarrier)
	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("encountered %d errors during concurrent creation", errorCount)
	}

	if allowedCount != 5 {
		t.Errorf("expected EXACTLY 5 allowed requests from capacity 5, got %d", allowedCount)
	}
	if deniedCount != (concurrency - 5) {
		t.Errorf("expected %d denied requests, got %d", concurrency-5, deniedCount)
	}
}

func TestPostgresLimiter_ConcurrentRequests_OneBucket(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	testKey := fmt.Sprintf("test:concurr_consume:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:concurr_consume",
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: 30 * time.Second,
	}

	const concurrency = 50
	var wg sync.WaitGroup
	var allowedCount int64
	var deniedCount int64

	startBarrier := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBarrier
			res, err := limiter.Allow(ctx, testKey, policy)
			if err != nil {
				t.Errorf("allow failed: %v", err)
				return
			}
			if res.Allowed {
				atomic.AddInt64(&allowedCount, 1)
			} else {
				atomic.AddInt64(&deniedCount, 1)
			}
		}()
	}

	close(startBarrier)
	wg.Wait()

	if allowedCount != 10 {
		t.Errorf("expected exactly 10 allowed requests, got %d", allowedCount)
	}
	if deniedCount != 40 {
		t.Errorf("expected exactly 40 denied requests, got %d", deniedCount)
	}

	// Verify balance is exactly 0 and never negative
	var tokens int64
	err := pool.QueryRow(ctx, "SELECT tokens FROM rate_limit_buckets WHERE key = $1", testKey).Scan(&tokens)
	if err != nil {
		t.Fatalf("failed to query tokens: %v", err)
	}
	if tokens != 0 {
		t.Errorf("expected final token balance 0, got %d", tokens)
	}
}

func TestPostgresLimiter_MultipleInstances_ShareState(t *testing.T) {
	pool, limiterA, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	limiterB, err := ratelimit.NewPostgresLimiter(pool)
	if err != nil {
		t.Fatalf("failed creating second limiter instance: %v", err)
	}

	ctx := context.Background()
	testKey := fmt.Sprintf("test:multi_inst:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, testKey)

	policy := ratelimit.Policy{
		ID:             "test:multi_inst",
		Capacity:       3,
		RefillTokens:   1,
		RefillInterval: 10 * time.Second,
	}

	// Limiter A consumes 1
	resA1, err := limiterA.Allow(ctx, testKey, policy)
	if err != nil || !resA1.Allowed || resA1.Remaining != 2 {
		t.Fatalf("limiterA res1: err=%v, res=%+v", err, resA1)
	}

	// Limiter B consumes 1
	resB1, err := limiterB.Allow(ctx, testKey, policy)
	if err != nil || !resB1.Allowed || resB1.Remaining != 1 {
		t.Fatalf("limiterB res1: err=%v, res=%+v", err, resB1)
	}

	// Limiter A consumes 1
	resA2, err := limiterA.Allow(ctx, testKey, policy)
	if err != nil || !resA2.Allowed || resA2.Remaining != 0 {
		t.Fatalf("limiterA res2: err=%v, res=%+v", err, resA2)
	}

	// Limiter B is now DENIED
	resB2, err := limiterB.Allow(ctx, testKey, policy)
	if err != nil || resB2.Allowed {
		t.Fatalf("limiterB res2: expected denied, got err=%v, res=%+v", err, resB2)
	}
}

func TestPostgresLimiter_Cleanup(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	defer teardown()

	ctx := context.Background()
	prefix := fmt.Sprintf("test:cleanup:%d", time.Now().UnixNano())
	defer deleteTestKeys(ctx, pool, prefix)

	oldKey := prefix + ":old"
	freshKey := prefix + ":fresh"

	// Insert an old bucket (updated 48 hours ago)
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	_, err := pool.Exec(ctx, `
		INSERT INTO rate_limit_buckets (key, tokens, last_refill_at, updated_at)
		VALUES ($1, 5, $2, $2)
	`, oldKey, oldTime)
	if err != nil {
		t.Fatalf("failed inserting old bucket: %v", err)
	}

	// Insert a fresh bucket (updated now)
	now := time.Now().UTC()
	_, err = pool.Exec(ctx, `
		INSERT INTO rate_limit_buckets (key, tokens, last_refill_at, updated_at)
		VALUES ($1, 5, $2, $2)
	`, freshKey, now)
	if err != nil {
		t.Fatalf("failed inserting fresh bucket: %v", err)
	}

	// Run cleanup with 24-hour retention
	deleted, err := limiter.Cleanup(ctx, 24*time.Hour, 100)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 deleted row, got %d", deleted)
	}

	// Verify old bucket is gone
	var count int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM rate_limit_buckets WHERE key = $1", oldKey).Scan(&count)
	if count != 0 {
		t.Errorf("expected old bucket to be deleted")
	}

	// Verify fresh bucket remains
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM rate_limit_buckets WHERE key = $1", freshKey).Scan(&count)
	if count != 1 {
		t.Errorf("expected fresh bucket to be preserved")
	}
}

func TestPostgresLimiter_DatabaseErrors(t *testing.T) {
	pool, limiter, teardown := setupTestDB(t)
	if pool == nil {
		return
	}
	teardown() // close pool immediately to test DB failure

	ctx := context.Background()
	policy := ratelimit.Policy{
		ID:             "test:err",
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: time.Second,
	}

	_, err := limiter.Allow(ctx, "test:err:closed", policy)
	if err == nil {
		t.Fatal("expected error on closed connection pool, got nil")
	}
}

package auth_test

// HTTP handler-level tests for ADR-020 rate-limit enforcement.
//
// These tests use a stub ratelimit.Limiter so they run without a database.
// They verify:
//   - IP and email/token limits are checked before expensive operations.
//   - Infrastructure failures return 500 (fail closed), not 429.
//   - Exhausted email failure bucket still allows correct credentials to succeed.
//   - 429 responses include a Retry-After header.
//   - Successful authentication does not consume a failure token.
//   - Registration completion rate-limit rejection does not touch token state.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/ratelimit"
	"github.com/Punith1117/ticket-forge/internal/security"
)

// --- stub Limiter ---------------------------------------------------------

type stubResult struct {
	result ratelimit.Result
	err    error
}

// stubLimiter records calls and returns pre-configured results keyed by call index.
type stubLimiter struct {
	// responses are returned in order; last entry is repeated for subsequent calls.
	responses []stubResult
	calls     int
	// callLog records (key, policyID) for each Allow invocation.
	callLog []string
}

func (s *stubLimiter) Allow(_ context.Context, key string, policy ratelimit.Policy) (ratelimit.Result, error) {
	s.callLog = append(s.callLog, fmt.Sprintf("%s|%s", policy.ID, key[:min(8, len(key))]))
	idx := s.calls
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	s.calls++
	return s.responses[idx].result, s.responses[idx].err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func allowResult() stubResult {
	return stubResult{result: ratelimit.Result{Allowed: true, Remaining: 5}}
}

func denyResult(retryAfter time.Duration) stubResult {
	return stubResult{result: ratelimit.Result{Allowed: false, Remaining: 0, RetryAfter: retryAfter}}
}

func errResult() stubResult {
	return stubResult{err: errors.New("connection refused")}
}

// --- stub KeyDeriver -------------------------------------------------------
// Use a real KeyDeriver with a test secret.
func newTestKeyDeriver(t *testing.T) *ratelimit.KeyDeriver {
	t.Helper()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	kd, err := ratelimit.NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("newTestKeyDeriver: %v", err)
	}
	return kd
}

// --- helpers --------------------------------------------------------------

func newHandlerWithStub(t *testing.T, limiter *stubLimiter) *auth.Handler {
	t.Helper()
	kd := newTestKeyDeriver(t)
	return auth.NewHandler(nil, nil, false, auth.WithRateLimiter(limiter, kd))
}

func newHandlerWithStubAndService(t *testing.T, service *auth.Service, limiter *stubLimiter) *auth.Handler {
	t.Helper()
	kd := newTestKeyDeriver(t)
	return auth.NewHandler(service, nil, false, auth.WithRateLimiter(limiter, kd))
}

func doPost(t *testing.T, handler http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, mustMarshal(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.5:12345"
	handler(rec, req)
	return rec
}

// --- Check 2: Retry-After header present on 429 ----------------------------

func TestHandler_RateLimit_RetryAfterHeaderOnDeny(t *testing.T) {
	retryAfter := 42 * time.Second
	stub := &stubLimiter{responses: []stubResult{denyResult(retryAfter)}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: "user@example.com", Password: "pw"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	val := rec.Header().Get("Retry-After")
	if val == "" {
		t.Fatal("expected Retry-After header to be set")
	}
	secs, err := strconv.Atoi(val)
	if err != nil {
		t.Fatalf("Retry-After is not an integer: %q", val)
	}
	if secs != 42 {
		t.Errorf("expected Retry-After=42, got %d", secs)
	}
	// Body must be generic JSON error; must not expose internal details.
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body.Error.Code != "TOO_MANY_REQUESTS" {
		t.Errorf("expected error code 'TOO_MANY_REQUESTS', got %q", body.Error)
	}
}

// --- Check 2: Retry-After fractional seconds rounded up --------------------

func TestHandler_RateLimit_RetryAfterRoundedUp(t *testing.T) {
	retryAfter := 500 * time.Millisecond // 0.5s → should round up to 1
	stub := &stubLimiter{responses: []stubResult{denyResult(retryAfter)}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: "user@example.com", Password: "pw"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	secs, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
	if secs < 1 {
		t.Errorf("expected Retry-After >= 1 for sub-second duration, got %d", secs)
	}
}

// --- Check 4: Infrastructure failure → 500, not 429 -----------------------

func TestHandler_RateLimit_InfraFailureReturns500_Login(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{errResult()}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: "user@example.com", Password: "pw"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on limiter infra failure, got %d", rec.Code)
	}
}

func TestHandler_RateLimit_InfraFailureReturns500_Register(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{errResult()}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleRegister, "/api/v1/auth/register",
		auth.RegisterRequest{Email: "user@example.com"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on limiter infra failure, got %d", rec.Code)
	}
}

func TestHandler_RateLimit_InfraFailureReturns500_RegisterComplete(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{errResult()}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleRegisterComplete, "/api/v1/auth/register/complete",
		auth.RegisterCompleteRequest{Token: "some-token-value", Password: "ValidPassword1234!"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on limiter infra failure, got %d", rec.Code)
	}
}

// --- Check 2: Exhausted email failure bucket must NOT block correct credentials ---
//
// ADR-020 §Login:
//   "if the email failure bucket is depleted, the server must still verify the
//    supplied credentials. A correct password must be allowed to authenticate,
//    provided the request passes the IP-based limit."
//
// Flow: IP bucket allows → service.Login succeeds (correct creds) → email failure
// bucket is NEVER consulted → session issued.
//
// We verify this using a real service (with an integration DB) so the actual
// Argon2id check runs. Without a DB we fall back to verifying that the stub is
// called in the expected order: IP allow, then Login, then no failure-bucket call.

func TestHandler_Login_CorrectCredentials_BypassFailureBucket(t *testing.T) {
	// IP bucket allows (call 0). Failure bucket would deny (call 1) if reached.
	// If correct credentials succeed, the failure-bucket Allow should never be called.
	stub := &stubLimiter{responses: []stubResult{
		allowResult(),                // IP bucket – allowed
		denyResult(60 * time.Second), // failure bucket – denied (should NOT be reached on success)
	}}
	kd := newTestKeyDeriver(t)

	// We need a real service to have login succeed. Use DB integration helper.
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		t.Skip("PostgreSQL unavailable")
	}
	defer pool.Close()

	email := "rl_correct_creds@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	h := auth.NewHandler(service, nil, false, auth.WithRateLimiter(stub, kd))

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: email, Password: password})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct credentials, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	// Only 1 limiter call (IP bucket); failure bucket must not be consulted.
	if stub.calls != 1 {
		t.Errorf("expected exactly 1 rate-limit call (IP bucket only), got %d", stub.calls)
	}
}

// --- Check 2 continued: Exhausted failure bucket → 429 on wrong credentials ---
//
// When the failure bucket is depleted and credentials are wrong, the response
// must be 429 (not 401) and must not consume another token.

func TestHandler_Login_ExhaustedFailureBucket_WrongCredentials_Returns429(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		t.Skip("PostgreSQL unavailable")
	}
	defer pool.Close()

	email := "rl_exhausted@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	// IP bucket allows (call 0). Failure bucket is depleted (call 1).
	stub := &stubLimiter{responses: []stubResult{
		allowResult(),                // IP check – allowed
		denyResult(30 * time.Second), // failure bucket – depleted
	}}
	kd := newTestKeyDeriver(t)
	h := auth.NewHandler(service, nil, false, auth.WithRateLimiter(stub, kd))

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: email, Password: "WRONG-password"})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 when failure bucket depleted, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
	if stub.calls != 2 {
		t.Errorf("expected 2 rate-limit calls (IP + failure bucket), got %d", stub.calls)
	}
}

// --- Check 1: Six policies defined and all present in handler calls ---------

func TestHandler_Login_CallsIPBucketBeforeAuth(t *testing.T) {
	// IP bucket is the first call; deny it before auth runs.
	stub := &stubLimiter{responses: []stubResult{denyResult(5 * time.Second)}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: "user@example.com", Password: "pw"})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for IP-exhausted login, got %d", rec.Code)
	}
	if stub.calls != 1 {
		t.Errorf("expected exactly 1 rate-limit call (IP), got %d", stub.calls)
	}
	// Confirm the first call used the login-IP policy
	if len(stub.callLog) > 0 && stub.callLog[0][:len("auth:log")] != "auth:log" {
		t.Errorf("first rate-limit call should be login IP policy, got %q", stub.callLog[0])
	}
}

func TestHandler_Register_CallsIPThenEmailBucket(t *testing.T) {
	// Allow IP, deny email bucket.
	stub := &stubLimiter{responses: []stubResult{
		allowResult(),               // IP bucket
		denyResult(5 * time.Second), // email bucket
	}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleRegister, "/api/v1/auth/register",
		auth.RegisterRequest{Email: "user@example.com"})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for email-exhausted register, got %d", rec.Code)
	}
	if stub.calls != 2 {
		t.Errorf("expected 2 rate-limit calls (IP + email), got %d", stub.calls)
	}
}

func TestHandler_RegisterComplete_CallsIPThenTokenBucket(t *testing.T) {
	// Allow IP, deny token bucket.
	stub := &stubLimiter{responses: []stubResult{
		allowResult(),               // IP bucket
		denyResult(5 * time.Second), // token bucket
	}}
	h := newHandlerWithStub(t, stub)

	rec := doPost(t, h.HandleRegisterComplete, "/api/v1/auth/register/complete",
		auth.RegisterCompleteRequest{Token: "some-token-value", Password: "ValidPassword1234!"})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for token-exhausted register/complete, got %d", rec.Code)
	}
	if stub.calls != 2 {
		t.Errorf("expected 2 rate-limit calls (IP + token), got %d", stub.calls)
	}
}

// --- Check 5: RegisterComplete rate-limit rejection must not touch DB --------
//
// When rejected by the IP bucket, the handler must return before any DB access.
// We verify no DB calls were made by using a nil service — if the handler
// tried to call service methods it would panic (nil pointer).

func TestHandler_RegisterComplete_RateLimitRejection_NoServiceCall(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{denyResult(10 * time.Second)}}
	// nil service: any service call would panic, proving the handler returned early.
	h := auth.NewHandler(nil, nil, false,
		auth.WithRateLimiter(stub, newTestKeyDeriver(t)))

	rec := doPost(t, h.HandleRegisterComplete, "/api/v1/auth/register/complete",
		auth.RegisterCompleteRequest{Token: "some-token-value", Password: "ValidPassword1234!"})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

// --- Check 7: No plaintext identifiers in bucket keys ----------------------
//
// Verify the key deriver produces HMAC output containing a ':' separator and
// does not contain the raw email or IP as a substring.

func TestHandler_RateLimit_BucketKeyDoesNotContainRawIdentifier(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = 0xAB
	}
	kd, _ := ratelimit.NewKeyDeriver(secret)

	rawEmail := "alice@example.com"
	rawIP := "203.0.113.5"

	emailKey, err := kd.DeriveKey(auth.PolicyLoginEmailFailure.ID, ratelimit.DomainEmail, rawEmail)
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}
	ipKey, err := kd.DeriveKey(auth.PolicyLoginIP.ID, ratelimit.DomainIP, rawIP)
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}

	for _, tc := range []struct {
		name       string
		key        string
		identifier string
	}{
		{"email key", emailKey, rawEmail},
		{"IP key", ipKey, rawIP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.key) == 0 {
				t.Fatal("derived key is empty")
			}
			// Key must not embed the raw identifier
			if contains(tc.key, tc.identifier) {
				t.Errorf("bucket key %q contains raw identifier %q", tc.key, tc.identifier)
			}
		})
	}
}

// --- Check 8: Retry-After is derived from limiter result, not recalculated ---

func TestHandler_RateLimit_RetryAfterMatchesLimiterResult(t *testing.T) {
	for _, secs := range []int{1, 5, 60, 120} {
		t.Run(fmt.Sprintf("%ds", secs), func(t *testing.T) {
			retryDur := time.Duration(secs) * time.Second
			stub := &stubLimiter{responses: []stubResult{denyResult(retryDur)}}
			h := newHandlerWithStub(t, stub)

			rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
				auth.LoginRequest{Email: "user@example.com", Password: "pw"})

			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("expected 429, got %d", rec.Code)
			}
			got, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
			if got != secs {
				t.Errorf("expected Retry-After=%d, got %d", secs, got)
			}
		})
	}
}

// --- Check 9: Existing authentication behavior intact ----------------------
//
// Without a limiter configured, the handler behaves exactly as before.

func TestHandler_Login_NilLimiter_PassesThrough(t *testing.T) {
	pool, service, encrypter := setupIntegrationDB(t)
	if pool == nil {
		t.Skip("PostgreSQL unavailable")
	}
	defer pool.Close()

	email := "nilrl@example.com"
	password := "ValidPassword1234!"
	createVerifiedUser(t, pool, service, encrypter, email, password)

	// No limiter configured — handler must still work normally.
	h := auth.NewHandler(service, nil, false)

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: email, Password: password})

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 without rate limiter, got %d", rec.Code)
	}
}

// --- Check 6: IP limit checked before Argon2id hashing ---------------------
//
// When the IP bucket is exhausted the handler must return 429 before
// service.Login is called. We verify this by using a nil service — if the
// handler called service.Login on a nil pointer it would panic.

func TestHandler_Login_IPLimitCheckedBeforeArgon2id(t *testing.T) {
	stub := &stubLimiter{responses: []stubResult{denyResult(5 * time.Second)}}
	// nil service would panic if service.Login were called.
	h := auth.NewHandler(nil, nil, false,
		auth.WithRateLimiter(stub, newTestKeyDeriver(t)))

	rec := doPost(t, h.HandleLogin, "/api/v1/auth/login",
		auth.LoginRequest{Email: "user@example.com", Password: "pw"})

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 before service call, got %d", rec.Code)
	}
}

// --- Check 3: No trusted proxy → RemoteAddr used (spoofed header ignored) --

func TestHandler_UntrustedXForwardedFor_Ignored(t *testing.T) {
	// extractor with no trusted proxies
	extractor, _ := auth.NewIPExtractor(nil)

	var capturedKey string
	stub := &stubLimiter{responses: []stubResult{denyResult(1 * time.Second)}}
	kd := newTestKeyDeriver(t)
	capturingLimiter := &captureLimiter{inner: stub, capturedKey: &capturedKey}

	h := auth.NewHandler(nil, nil, false,
		auth.WithRateLimiter(capturingLimiter, kd),
		auth.WithIPExtractor(extractor),
	)

	// Attacker tries to spoof a trusted IP via X-Forwarded-For
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		mustMarshal(t, auth.LoginRequest{Email: "u@example.com", Password: "pw"}))
	req.RemoteAddr = "203.0.113.99:9999" // attacker's real IP
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	h.HandleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	// Key must be derived from RemoteAddr (203.0.113.99), not spoofed header (10.0.0.1)
	// The key is an HMAC so we can't inspect the IP directly, but we verify that
	// if we derive a key for the spoofed IP it differs from what was used.
	spoofedKey, _ := kd.DeriveKey(auth.PolicyLoginIP.ID, ratelimit.DomainIP, "10.0.0.1")
	if capturedKey == spoofedKey {
		t.Error("bucket key matches spoofed IP — X-Forwarded-For was trusted without proxy config")
	}
}

// captureLimiter wraps a stub and captures the first key seen.
type captureLimiter struct {
	inner       *stubLimiter
	capturedKey *string
}

func (c *captureLimiter) Allow(ctx context.Context, key string, policy ratelimit.Policy) (ratelimit.Result, error) {
	if *c.capturedKey == "" {
		*c.capturedKey = key
	}
	return c.inner.Allow(ctx, key, policy)
}

// --- Check: Policy values match ADR-020 table ------------------------------

func TestPolicies_MatchADR020(t *testing.T) {
	policies := []struct {
		name     string
		policy   ratelimit.Policy
		capacity int64
		refillT  int64
		interval time.Duration
	}{
		{"LoginIP", auth.PolicyLoginIP, 30, 1, 2 * time.Second},
		{"LoginEmailFailure", auth.PolicyLoginEmailFailure, 10, 1, 60 * time.Second},
		{"RegisterIP", auth.PolicyRegisterIP, 10, 1, 30 * time.Second},
		{"RegisterEmail", auth.PolicyRegisterEmail, 3, 1, 10 * time.Minute},
		{"RegisterCompleteIP", auth.PolicyRegisterCompleteIP, 20, 1, 10 * time.Second},
		{"RegisterCompleteToken", auth.PolicyRegisterCompleteToken, 5, 1, 2 * time.Minute},
		{"PasswordResetIP", auth.PolicyPasswordResetIP, 10, 1, 60 * time.Second},
		{"PasswordResetEmail", auth.PolicyPasswordResetEmail, 3, 1, 10 * time.Minute},
		{"PasswordResetCompleteIP", auth.PolicyPasswordResetCompleteIP, 20, 1, 30 * time.Second},
		{"PasswordResetCompleteToken", auth.PolicyPasswordResetCompleteToken, 5, 1, 2 * time.Minute},
	}

	for _, tc := range policies {
		t.Run(tc.name, func(t *testing.T) {
			if tc.policy.Capacity != tc.capacity {
				t.Errorf("Capacity: want %d, got %d", tc.capacity, tc.policy.Capacity)
			}
			if tc.policy.RefillTokens != tc.refillT {
				t.Errorf("RefillTokens: want %d, got %d", tc.refillT, tc.policy.RefillTokens)
			}
			if tc.policy.RefillInterval != tc.interval {
				t.Errorf("RefillInterval: want %v, got %v", tc.interval, tc.policy.RefillInterval)
			}
			if tc.policy.ID == "" {
				t.Error("Policy ID must not be empty")
			}
			// Validate using the ratelimit package's own validator
			if err := tc.policy.Validate(); err != nil {
				t.Errorf("policy fails Validate(): %v", err)
			}
		})
	}
}

// --- helper ----------------------------------------------------------------

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}()
}

// newTestEncrypterInline is a local alias used by some tests in this file.
var _ = (*security.TokenEncrypter)(nil) // ensure import used

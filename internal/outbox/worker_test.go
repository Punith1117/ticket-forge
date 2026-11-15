package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/email"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type mockRepository struct {
	claimFunc    func(ctx context.Context, supportedEventTypes []string, limit int32, leaseDuration time.Duration) ([]sqlc.OutboxEvent, error)
	completeFunc func(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, providerMsgID string) (bool, error)
	retryFunc    func(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, nextAttemptAt time.Time, lastError string) (bool, error)
	failFunc     func(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error)
	insertFunc   func(ctx context.Context, aggregateType string, aggregateID pgtype.UUID, eventType string, payload []byte) (sqlc.OutboxEvent, error)
}

func (m *mockRepository) ClaimEligibleEvents(ctx context.Context, supportedEventTypes []string, limit int32, leaseDuration time.Duration) ([]sqlc.OutboxEvent, error) {
	if m.claimFunc != nil {
		return m.claimFunc(ctx, supportedEventTypes, limit, leaseDuration)
	}
	return nil, nil
}

func (m *mockRepository) CompleteEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, providerMsgID string) (bool, error) {
	if m.completeFunc != nil {
		return m.completeFunc(ctx, id, claimToken, providerMsgID)
	}
	return true, nil
}

func (m *mockRepository) RetryEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, nextAttemptAt time.Time, lastError string) (bool, error) {
	if m.retryFunc != nil {
		return m.retryFunc(ctx, id, claimToken, nextAttemptAt, lastError)
	}
	return true, nil
}

func (m *mockRepository) FailEvent(ctx context.Context, id pgtype.UUID, claimToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
	if m.failFunc != nil {
		return m.failFunc(ctx, id, claimToken, deliveryAttempted, lastError)
	}
	return true, nil
}

func (m *mockRepository) InsertEvent(ctx context.Context, aggregateType string, aggregateID pgtype.UUID, eventType string, payload []byte) (sqlc.OutboxEvent, error) {
	if m.insertFunc != nil {
		return m.insertFunc(ctx, aggregateType, aggregateID, eventType, payload)
	}
	return sqlc.OutboxEvent{}, nil
}

type mockSender struct {
	mu       sync.Mutex
	sendFunc func(ctx context.Context, msg email.Message) error
	sent     []email.Message
}

func (m *mockSender) Send(ctx context.Context, msg email.Message) error {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	if m.sendFunc != nil {
		return m.sendFunc(ctx, msg)
	}
	return nil
}

func testSetup(t *testing.T) (*security.TokenEncrypter, outbox.WorkerConfig) {
	t.Helper()
	keyHex, err := security.GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("failed to generate encryption key: %v", err)
	}
	keyBytes, err := security.ParseKey(keyHex)
	if err != nil {
		t.Fatalf("failed to parse test key: %v", err)
	}
	ring, err := security.NewKeyRing(1, map[int][]byte{1: keyBytes})
	if err != nil {
		t.Fatalf("failed to create keyring: %v", err)
	}
	encrypter, err := security.NewTokenEncrypter(ring)
	if err != nil {
		t.Fatalf("failed to create encrypter: %v", err)
	}

	cfg := outbox.WorkerConfigFromEnv()
	cfg.PollInterval = 10 * time.Millisecond

	return encrypter, cfg
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func TestWorker_ProcessEvent_Success_RegistrationVerification(t *testing.T) {
	encrypter, cfg := testSetup(t)
	rawToken := "test-verification-token-1234567890"
	encrypted, err := encrypter.EncryptToken(rawToken, []byte(security.PurposeRegistrationVerification))
	if err != nil {
		t.Fatalf("failed to encrypt token: %v", err)
	}

	payloadJSON, err := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "user@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	eventID := uuid.New()
	claimToken := uuid.New()
	event := sqlc.OutboxEvent{
		ID:         pgUUID(eventID),
		ClaimToken: pgUUID(claimToken),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
		Attempts:   0,
	}

	completedCalled := false
	repo := &mockRepository{
		completeFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, providerMsgID string) (bool, error) {
			if id.Bytes != eventID || cToken.Bytes != claimToken {
				t.Errorf("completeFunc called with wrong ID or claim token")
			}
			completedCalled = true
			return true, nil
		},
		failFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
			t.Fatalf("failFunc called unexpectedly: %s", lastError)
			return false, nil
		},
		retryFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, nextAttemptAt time.Time, lastError string) (bool, error) {
			t.Fatalf("retryFunc called unexpectedly: %s", lastError)
			return false, nil
		},
	}

	sender := &mockSender{}
	worker, err := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	worker.ProcessEvent(context.Background(), event)

	if !completedCalled {
		t.Error("expected CompleteEvent to be called")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(sender.sent))
	}

	sentMsg := sender.sent[0]
	if sentMsg.To[0] != "user@example.com" {
		t.Errorf("expected recipient user@example.com, got %s", sentMsg.To[0])
	}
	if !strings.Contains(sentMsg.TextBody, rawToken) {
		t.Errorf("expected plain text body to contain raw token")
	}
	if !strings.Contains(sentMsg.HTMLBody, rawToken) {
		t.Errorf("expected html body to contain raw token")
	}
	if !strings.Contains(sentMsg.TextBody, "/verify-email?token=") {
		t.Errorf("expected verification URL in text body")
	}
}

func TestWorker_ProcessEvent_Success_PasswordReset(t *testing.T) {
	encrypter, cfg := testSetup(t)
	rawToken := "test-reset-token-9876543210"
	encrypted, err := encrypter.EncryptToken(rawToken, []byte(security.PurposePasswordReset))
	if err != nil {
		t.Fatalf("failed to encrypt token: %v", err)
	}

	payloadJSON, err := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "reset@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	eventID := uuid.New()
	claimToken := uuid.New()
	event := sqlc.OutboxEvent{
		ID:         pgUUID(eventID),
		ClaimToken: pgUUID(claimToken),
		EventType:  outbox.EventTypePasswordReset,
		Payload:    payloadJSON,
		Attempts:   0,
	}

	completedCalled := false
	repo := &mockRepository{
		completeFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, providerMsgID string) (bool, error) {
			completedCalled = true
			return true, nil
		},
	}

	sender := &mockSender{}
	worker, err := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	worker.ProcessEvent(context.Background(), event)

	if !completedCalled {
		t.Error("expected CompleteEvent to be called")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(sender.sent))
	}

	sentMsg := sender.sent[0]
	if sentMsg.To[0] != "reset@example.com" {
		t.Errorf("expected recipient reset@example.com, got %s", sentMsg.To[0])
	}
	if !strings.Contains(sentMsg.TextBody, "/reset-password?token=") {
		t.Errorf("expected reset URL in text body")
	}
}

func TestWorker_ProcessEvent_TokenExpiredBeforeDelivery(t *testing.T) {
	encrypter, cfg := testSetup(t)
	encrypted, err := encrypter.EncryptToken("expired-token", []byte(security.PurposeRegistrationVerification))
	if err != nil {
		t.Fatalf("failed to encrypt token: %v", err)
	}

	// Token expired 5 minutes ago
	payloadJSON, _ := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "expired@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(-5 * time.Minute),
	})

	event := sqlc.OutboxEvent{
		ID:         pgUUID(uuid.New()),
		ClaimToken: pgUUID(uuid.New()),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
		Attempts:   0,
	}

	failedCalled := false
	repo := &mockRepository{
		failFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
			failedCalled = true
			if deliveryAttempted {
				t.Errorf("deliveryAttempted should be false for expired token")
			}
			if !strings.Contains(lastError, "expired") {
				t.Errorf("expected lastError to mention expired, got: %s", lastError)
			}
			return true, nil
		},
	}

	sender := &mockSender{}
	worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	worker.ProcessEvent(context.Background(), event)

	if !failedCalled {
		t.Error("expected FailEvent to be called")
	}
	if len(sender.sent) != 0 {
		t.Error("sender should NOT be called for expired token")
	}
}

func TestWorker_ProcessEvent_MalformedPayload(t *testing.T) {
	encrypter, cfg := testSetup(t)

	testCases := []struct {
		name    string
		payload []byte
	}{
		{"invalid json", []byte("invalid-json{")},
		{"missing recipient", []byte(`{"encrypted_token":"abc","token_expires_at":"2030-01-01T00:00:00Z"}`)},
		{"missing encrypted_token", []byte(`{"recipient":"test@example.com","token_expires_at":"2030-01-01T00:00:00Z"}`)},
		{"missing token_expires_at", []byte(`{"recipient":"test@example.com","encrypted_token":"abc"}`)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			failedCalled := false
			repo := &mockRepository{
				failFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
					failedCalled = true
					if deliveryAttempted {
						t.Errorf("deliveryAttempted should be false")
					}
					return true, nil
				},
			}

			sender := &mockSender{}
			worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)

			event := sqlc.OutboxEvent{
				ID:         pgUUID(uuid.New()),
				ClaimToken: pgUUID(uuid.New()),
				EventType:  outbox.EventTypeRegistrationVerification,
				Payload:    tc.payload,
			}

			worker.ProcessEvent(context.Background(), event)

			if !failedCalled {
				t.Errorf("[%s] expected FailEvent to be called", tc.name)
			}
			if len(sender.sent) != 0 {
				t.Errorf("[%s] sender should NOT have been called", tc.name)
			}
		})
	}
}

func TestWorker_ProcessEvent_DecryptionFailure(t *testing.T) {
	encrypter, cfg := testSetup(t)

	// Encrypted with purpose password_reset, but event is registration_verification -> AAD mismatch!
	encrypted, err := encrypter.EncryptToken("secret-token", []byte(security.PurposePasswordReset))
	if err != nil {
		t.Fatalf("failed to encrypt token: %v", err)
	}

	payloadJSON, _ := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "user@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})

	failedCalled := false
	repo := &mockRepository{
		failFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
			failedCalled = true
			if deliveryAttempted {
				t.Errorf("deliveryAttempted should be false for decryption failure")
			}
			if !strings.Contains(lastError, "decryption failed") {
				t.Errorf("expected lastError to mention decryption failed, got: %s", lastError)
			}
			return true, nil
		},
	}

	sender := &mockSender{}
	worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)

	event := sqlc.OutboxEvent{
		ID:         pgUUID(uuid.New()),
		ClaimToken: pgUUID(uuid.New()),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
	}

	worker.ProcessEvent(context.Background(), event)

	if !failedCalled {
		t.Error("expected FailEvent to be called on decryption failure")
	}
	if len(sender.sent) != 0 {
		t.Error("sender should NOT be called on decryption failure")
	}
}

func TestWorker_ProcessEvent_TransientError_Retry(t *testing.T) {
	encrypter, cfg := testSetup(t)
	rawToken := "valid-token"
	encrypted, _ := encrypter.EncryptToken(rawToken, []byte(security.PurposeRegistrationVerification))

	payloadJSON, _ := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "retry@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	})

	event := sqlc.OutboxEvent{
		ID:         pgUUID(uuid.New()),
		ClaimToken: pgUUID(uuid.New()),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
		Attempts:   0, // First attempt failed
	}

	retryCalled := false
	repo := &mockRepository{
		retryFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, nextAttemptAt time.Time, lastError string) (bool, error) {
			retryCalled = true
			if nextAttemptAt.Before(time.Now().UTC()) {
				t.Errorf("expected nextAttemptAt to be in future, got %v", nextAttemptAt)
			}
			if !strings.Contains(lastError, "connection refused") {
				t.Errorf("expected lastError to contain network error, got %s", lastError)
			}
			return true, nil
		},
	}

	sender := &mockSender{
		sendFunc: func(ctx context.Context, msg email.Message) error {
			return &net.OpError{Op: "dial", Err: errors.New("connection refused")}
		},
	}

	worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	worker.ProcessEvent(context.Background(), event)

	if !retryCalled {
		t.Error("expected RetryEvent to be called for transient failure")
	}
}

func TestWorker_ProcessEvent_TransientError_MaxRetriesExceeded(t *testing.T) {
	encrypter, cfg := testSetup(t)
	cfg.MaxRetries = 3

	rawToken := "valid-token"
	encrypted, _ := encrypter.EncryptToken(rawToken, []byte(security.PurposeRegistrationVerification))

	payloadJSON, _ := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "maxretries@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	})

	// Attempts is already 2; this attempt will be attempt 3, which equals MaxRetries
	event := sqlc.OutboxEvent{
		ID:         pgUUID(uuid.New()),
		ClaimToken: pgUUID(uuid.New()),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
		Attempts:   2,
	}

	failedCalled := false
	repo := &mockRepository{
		failFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
			failedCalled = true
			if !deliveryAttempted {
				t.Errorf("deliveryAttempted should be true when delivery was attempted")
			}
			if !strings.Contains(lastError, "max retries (3) exceeded") {
				t.Errorf("expected lastError to mention max retries exceeded, got: %s", lastError)
			}
			return true, nil
		},
	}

	sender := &mockSender{
		sendFunc: func(ctx context.Context, msg email.Message) error {
			return &net.OpError{Op: "dial", Err: errors.New("connection refused")}
		},
	}

	worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	worker.ProcessEvent(context.Background(), event)

	if !failedCalled {
		t.Error("expected FailEvent to be called when max retries exceeded")
	}
}

func TestWorker_ProcessEvent_PermanentError(t *testing.T) {
	encrypter, cfg := testSetup(t)
	rawToken := "valid-token"
	encrypted, _ := encrypter.EncryptToken(rawToken, []byte(security.PurposeRegistrationVerification))

	payloadJSON, _ := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "rejected@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	})

	event := sqlc.OutboxEvent{
		ID:         pgUUID(uuid.New()),
		ClaimToken: pgUUID(uuid.New()),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
		Attempts:   0,
	}

	failedCalled := false
	repo := &mockRepository{
		failFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, deliveryAttempted bool, lastError string) (bool, error) {
			failedCalled = true
			if !deliveryAttempted {
				t.Errorf("deliveryAttempted should be true for SMTP failure")
			}
			if !strings.Contains(lastError, "550") {
				t.Errorf("expected error to include 550 code, got: %s", lastError)
			}
			return true, nil
		},
	}

	sender := &mockSender{
		sendFunc: func(ctx context.Context, msg email.Message) error {
			return &textproto.Error{Code: 550, Msg: "User unknown"}
		},
	}

	worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	worker.ProcessEvent(context.Background(), event)

	if !failedCalled {
		t.Error("expected FailEvent to be called on permanent 550 error")
	}
}

func TestWorker_ProcessEvent_StaleWorkerFencing(t *testing.T) {
	encrypter, cfg := testSetup(t)
	rawToken := "valid-token"
	encrypted, _ := encrypter.EncryptToken(rawToken, []byte(security.PurposeRegistrationVerification))

	payloadJSON, _ := json.Marshal(outbox.EmailEventPayload{
		Recipient:      "user@example.com",
		EncryptedToken: encrypted,
		TokenExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	})

	event := sqlc.OutboxEvent{
		ID:         pgUUID(uuid.New()),
		ClaimToken: pgUUID(uuid.New()),
		EventType:  outbox.EventTypeRegistrationVerification,
		Payload:    payloadJSON,
		Attempts:   0,
	}

	// CompleteEvent returns false (0 rows updated because another worker took the lease)
	repo := &mockRepository{
		completeFunc: func(ctx context.Context, id pgtype.UUID, cToken pgtype.UUID, providerMsgID string) (bool, error) {
			return false, nil
		},
	}

	sender := &mockSender{}
	worker, _ := outbox.NewWorker(repo, sender, encrypter, cfg, nil)

	// Should execute gracefully without panicking or failing
	worker.ProcessEvent(context.Background(), event)
}

func TestWorker_Run_GracefulShutdown(t *testing.T) {
	encrypter, cfg := testSetup(t)
	cfg.PollInterval = 10 * time.Millisecond

	repo := &mockRepository{
		claimFunc: func(ctx context.Context, supportedEventTypes []string, limit int32, leaseDuration time.Duration) ([]sqlc.OutboxEvent, error) {
			return nil, nil
		},
	}

	sender := &mockSender{}
	worker, err := outbox.NewWorker(repo, sender, encrypter, cfg, nil)
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- worker.Run(ctx)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("worker.Run returned unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("worker.Run did not shut down in time")
	}
}

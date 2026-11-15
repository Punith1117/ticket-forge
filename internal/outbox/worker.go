package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/textproto"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/email"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Worker processes pending email events from the transactional outbox and dispatches them.
type Worker struct {
	repo      Repository
	sender    email.Sender
	encrypter *security.TokenEncrypter
	config    WorkerConfig
	logger    *slog.Logger
}

// NewWorker constructs and validates an outbox Worker.
func NewWorker(repo Repository, sender email.Sender, encrypter *security.TokenEncrypter, cfg WorkerConfig, logger *slog.Logger) (*Worker, error) {
	if repo == nil {
		return nil, errors.New("worker: repository cannot be nil")
	}
	if sender == nil {
		return nil, errors.New("worker: email sender cannot be nil")
	}
	if encrypter == nil {
		return nil, errors.New("worker: token encrypter cannot be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Worker{
		repo:      repo,
		sender:    sender,
		encrypter: encrypter,
		config:    cfg,
		logger:    logger,
	}, nil
}

// Run executes the worker polling loop until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	w.logger.Info("email outbox worker running",
		"poll_interval", w.config.PollInterval,
		"batch_size", w.config.BatchSize,
		"lease_duration", w.config.LeaseDuration,
	)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("email outbox worker stopping gracefully")
			return nil
		case <-ticker.C:
			for {
				count, err := w.ProcessNextBatch(ctx)
				if err != nil {
					if errors.Is(err, context.Canceled) || ctx.Err() != nil {
						return nil
					}
					w.logger.Error("error processing outbox batch", "error", err)
					break
				}
				// If a full batch was processed, immediately look for more work without sleeping
				if count < int(w.config.BatchSize) || ctx.Err() != nil {
					break
				}
			}
		}
	}
}

// ProcessNextBatch claims and processes up to BatchSize eligible events.
// Returns the number of events claimed and processed.
func (w *Worker) ProcessNextBatch(ctx context.Context) (int, error) {
	events, err := w.repo.ClaimEligibleEvents(ctx, w.config.SupportedEvents, w.config.BatchSize, w.config.LeaseDuration)
	if err != nil {
		return 0, fmt.Errorf("claiming outbox events: %w", err)
	}

	if len(events) == 0 {
		return 0, nil
	}

	for _, event := range events {
		if ctx.Err() != nil {
			return len(events), ctx.Err()
		}
		w.ProcessEvent(ctx, event)
	}

	return len(events), nil
}

// ProcessEvent executes the delivery lifecycle for a single claimed outbox event.
func (w *Worker) ProcessEvent(ctx context.Context, event sqlc.OutboxEvent) {
	eventIDStr := uuidString(event.ID)

	var aad []byte
	switch event.EventType {
	case EventTypeRegistrationVerification:
		aad = []byte(security.PurposeRegistrationVerification)
	case EventTypePasswordReset:
		aad = []byte(security.PurposePasswordReset)
	default:
		w.logger.Error("unsupported event type claimed by email worker", "event_id", eventIDStr, "event_type", event.EventType)
		_, _ = w.repo.FailEvent(ctx, event.ID, event.ClaimToken, false, fmt.Sprintf("unsupported event type: %s", event.EventType))
		return
	}

	// 1. Validate and parse payload
	payload, err := ParseEmailPayload(event.Payload)
	if err != nil {
		w.logger.Error("invalid outbox payload", "event_id", eventIDStr, "error", err)
		_, _ = w.repo.FailEvent(ctx, event.ID, event.ClaimToken, false, fmt.Sprintf("invalid payload: %v", err))
		return
	}

	// 2. Check token expiration before attempting delivery
	if time.Now().UTC().After(payload.TokenExpiresAt.UTC()) {
		w.logger.Info("token expired before delivery, marking failed", "event_id", eventIDStr, "expires_at", payload.TokenExpiresAt)
		_, _ = w.repo.FailEvent(ctx, event.ID, event.ClaimToken, false, "token expired before delivery")
		return
	}

	// 3. Decrypt token using bound AAD
	rawToken, err := w.encrypter.DecryptToken(payload.EncryptedToken, aad)
	if err != nil {
		w.logger.Error("failed to decrypt token payload", "event_id", eventIDStr, "error", err)
		_, _ = w.repo.FailEvent(ctx, event.ID, event.ClaimToken, false, fmt.Sprintf("token decryption failed: %v", err))
		return
	}

	// 4. Construct email message using appropriate template
	var msg email.Message
	switch event.EventType {
	case EventTypeRegistrationVerification:
		verifyURL := fmt.Sprintf("%s/verify-email?token=%s", w.config.BaseURL, rawToken)
		msg = email.RegistrationVerificationEmail(w.config.From, payload.Recipient, verifyURL)
	case EventTypePasswordReset:
		resetURL := fmt.Sprintf("%s/reset-password?token=%s", w.config.BaseURL, rawToken)
		msg = email.PasswordResetEmail(w.config.From, payload.Recipient, resetURL)
	}

	// 5. Send message via configured email sender
	sendErr := w.sender.Send(ctx, msg)
	if sendErr == nil {
		// Delivery succeeded: mark published, scrub encrypted token
		updated, err := w.repo.CompleteEvent(ctx, event.ID, event.ClaimToken, "")
		if err != nil {
			w.logger.Error("failed to mark outbox event as completed", "event_id", eventIDStr, "error", err)
			return
		}
		if !updated {
			w.logger.Warn("stale worker fenced out on completion: lease was reclaimed", "event_id", eventIDStr)
			return
		}
		w.logger.Info("email delivery successful", "event_id", eventIDStr, "event_type", event.EventType)
		return
	}

	// 6. Handle delivery failure (actual attempt occurred)
	isTransient := isTransientError(sendErr)
	currentAttempts := event.Attempts + 1

	if isTransient && int(currentAttempts) < w.config.MaxRetries {
		backoff := CalculateBackoff(int(currentAttempts), w.config.BaseRetryDelay, w.config.MaxRetryDelay)
		nextAttempt := time.Now().UTC().Add(backoff)
		w.logger.Warn("transient email delivery failure, scheduling retry",
			"event_id", eventIDStr,
			"attempt", currentAttempts,
			"max_retries", w.config.MaxRetries,
			"backoff", backoff,
			"error", sendErr.Error(),
		)
		updated, err := w.repo.RetryEvent(ctx, event.ID, event.ClaimToken, nextAttempt, sendErr.Error())
		if err != nil {
			w.logger.Error("failed to schedule retry", "event_id", eventIDStr, "error", err)
			return
		}
		if !updated {
			w.logger.Warn("stale worker fenced out on retry update", "event_id", eventIDStr)
		}
		return
	}

	// Permanent failure or max retries exceeded
	errMsg := sendErr.Error()
	if isTransient {
		errMsg = fmt.Sprintf("max retries (%d) exceeded: %s", w.config.MaxRetries, errMsg)
	}
	w.logger.Error("email delivery failed permanently",
		"event_id", eventIDStr,
		"attempts", currentAttempts,
		"error", errMsg,
	)
	updated, err := w.repo.FailEvent(ctx, event.ID, event.ClaimToken, true, errMsg)
	if err != nil {
		w.logger.Error("failed to mark event as failed", "event_id", eventIDStr, "error", err)
		return
	}
	if !updated {
		w.logger.Warn("stale worker fenced out on failure update", "event_id", eventIDStr)
	}
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation or timeout is transient (network stall)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	// Message validation errors (e.g. malformed recipient format) are permanent
	if errors.Is(err, email.ErrInvalidMessage) {
		return false
	}

	// Check standard SMTP error codes
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		// 4xx: transient negative completion (e.g. 421 server busy, 450 mailbox busy)
		if tpErr.Code >= 400 && tpErr.Code < 500 {
			return true
		}
		// 5xx: permanent negative completion (e.g. 550 mailbox not found, 501 syntax error)
		if tpErr.Code >= 500 && tpErr.Code < 600 {
			return false
		}
	}

	// Network errors (dial, connection refused, reset) are transient
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return true
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	id, err := uuid.FromBytes(u.Bytes[:])
	if err != nil {
		return ""
	}
	return id.String()
}

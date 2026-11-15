package outbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Punith1117/ticket-forge/internal/security"
)

const (
	// EventTypeRegistrationVerification is published when an email verification link is requested for registration.
	EventTypeRegistrationVerification = "auth.registration_verification_requested"

	// EventTypePasswordReset is published when a password reset link is requested.
	EventTypePasswordReset = "auth.password_reset_requested"
)

var (
	// ErrInvalidPayload indicates that the event payload does not match the expected schema.
	ErrInvalidPayload = errors.New("outbox: invalid event payload")
)

// EmailEventPayload defines the JSON payload stored in outbox_events.payload for email events.
type EmailEventPayload struct {
	Recipient      string                  `json:"recipient"`
	TokenExpiresAt time.Time               `json:"token_expires_at"`
	EncryptedToken security.EncryptedToken `json:"encrypted_token"`
}

// ParseEmailPayload deserializes and validates an email event payload from JSON bytes.
func ParseEmailPayload(data []byte) (EmailEventPayload, error) {
	if len(data) == 0 {
		return EmailEventPayload{}, fmt.Errorf("%w: payload is empty", ErrInvalidPayload)
	}

	var p EmailEventPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return EmailEventPayload{}, fmt.Errorf("%w: failed to parse JSON: %v", ErrInvalidPayload, err)
	}

	if strings.TrimSpace(p.Recipient) == "" {
		return EmailEventPayload{}, fmt.Errorf("%w: recipient cannot be empty", ErrInvalidPayload)
	}
	if p.TokenExpiresAt.IsZero() {
		return EmailEventPayload{}, fmt.Errorf("%w: token_expires_at is required", ErrInvalidPayload)
	}
	if err := p.EncryptedToken.Validate(); err != nil {
		return EmailEventPayload{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}

	return p, nil
}

package payment

import (
	"context"
	"net/http"
	"time"
)

// PaymentOutcome represents the normalized result of a payment event.
type PaymentOutcome string

const (
	PaymentOutcomeSucceeded PaymentOutcome = "succeeded"
	PaymentOutcomeFailed    PaymentOutcome = "failed"
)

// CreateCheckoutSessionRequest holds normalized data to initiate a provider checkout session.
type CreateCheckoutSessionRequest struct {
	PaymentAttemptID string
	ReservationID    string
	AmountMinor      int64
	Currency         string
	ExpiresAt        time.Time
}

// CheckoutSession contains normalized checkout details returned by a provider.
type CheckoutSession struct {
	ProviderSessionID      string
	ProviderOrderReference string
	CheckoutURL            string
	ExpiresAt              time.Time
}

// WebhookEvent represents a verified, normalized provider event.
type WebhookEvent struct {
	EventID                  string
	Provider                 string
	ProviderOrderReference   string
	ProviderPaymentReference string
	Outcome                  PaymentOutcome
	EventType                string
	AmountMinor              int64
	Currency                 string
	FailureReason            string
	RawPayload               []byte
}

// Provider represents the capabilities required from an external payment provider.
type Provider interface {
	Name() string
	CreateCheckoutSession(ctx context.Context, req CreateCheckoutSessionRequest) (CheckoutSession, error)
	VerifyWebhook(payload []byte, headers http.Header) (WebhookEvent, error)
}

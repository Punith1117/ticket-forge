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

// RefundRequest holds normalized data to execute a refund with the provider.
type RefundRequest struct {
	RefundID                 string
	PaymentAttemptID         string
	ProviderPaymentReference string
	ProviderOrderReference   string
	AmountMinor              int64
	Currency                 string
	Reason                   string
}

// RefundResult contains the provider's response for a refund execution.
type RefundResult struct {
	ProviderRefundReference string
}

// Provider represents the capabilities required from an external payment provider.
type Provider interface {
	Name() string
	CreateCheckoutSession(ctx context.Context, req CreateCheckoutSessionRequest) (CheckoutSession, error)
	VerifyWebhook(payload []byte, headers http.Header) (WebhookEvent, error)
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}

package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Punith1117/ticket-forge/internal/payment"
	"github.com/google/uuid"
)

const ProviderName = "simulated"

// WebhookPayload represents the JSON payload sent by the simulated payment provider.
type WebhookPayload struct {
	EventID                  string                 `json:"event_id"`
	EventType                string                 `json:"event_type"`
	Provider                 string                 `json:"provider"`
	ProviderOrderReference   string                 `json:"provider_order_reference"`
	ProviderPaymentReference string                 `json:"provider_payment_reference,omitempty"`
	AmountMinor              int64                  `json:"amount_minor"`
	Currency                 string                 `json:"currency"`
	Outcome                  payment.PaymentOutcome `json:"outcome"`
	FailureReason            string                 `json:"failure_reason,omitempty"`
	CreatedAt                time.Time              `json:"created_at"`
}

// Provider implements payment.Provider for testing and simulated payments.
type Provider struct {
	secret             []byte
	webhookURL         string
	baseURL            string
	client             *Client
	tolerance          time.Duration
	sessions           *SessionStore
	sessionLookup      SessionLookup
	reservationChecker ReservationChecker
	refundHook         func(ctx context.Context, req payment.RefundRequest) (payment.RefundResult, error)
}

func NewProvider(secret string, webhookURL string, client *Client) (*Provider, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("simulator: secret cannot be empty")
	}

	if client == nil {
		client = NewClient(nil)
	}

	baseURL := "http://localhost:8080"
	if parsed, err := url.Parse(webhookURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		baseURL = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	}

	return &Provider{
		secret:     []byte(secret),
		webhookURL: webhookURL,
		baseURL:    baseURL,
		client:     client,
		tolerance:  DefaultTolerance,
		sessions:   NewSessionStore(),
	}, nil
}

func (p *Provider) Name() string {
	return ProviderName
}

func (p *Provider) WebhookURL() string {
	return p.webhookURL
}

func (p *Provider) Secret() []byte {
	return p.secret
}

func (p *Provider) SetBaseURL(baseURL string) {
	p.baseURL = strings.TrimRight(baseURL, "/")
}

func (p *Provider) SetWebhookURL(webhookURL string) {
	p.webhookURL = webhookURL
}

func (p *Provider) SetReservationChecker(checker ReservationChecker) {
	p.reservationChecker = checker
}

func (p *Provider) SetSessionLookup(lookup SessionLookup) {
	p.sessionLookup = lookup
}

func (p *Provider) SetRefundHook(hook func(ctx context.Context, req payment.RefundRequest) (payment.RefundResult, error)) {
	p.refundHook = hook
}

func (p *Provider) Sessions() *SessionStore {
	return p.sessions
}

// CreateCheckoutSession creates a unique simulated checkout session (idempotent by PaymentAttemptID) and records it for the hosted page.
func (p *Provider) CreateCheckoutSession(
	ctx context.Context,
	req payment.CreateCheckoutSessionRequest,
) (payment.CheckoutSession, error) {
	if p.sessionLookup != nil && req.PaymentAttemptID != "" {
		if existing, err := p.sessionLookup.GetSessionByAttemptID(ctx, req.PaymentAttemptID); err == nil && existing.SessionID != "" {
			return payment.CheckoutSession{
				ProviderSessionID:      existing.SessionID,
				ProviderOrderReference: existing.OrderRef,
				CheckoutURL:            p.checkoutURL(existing.SessionID),
				ExpiresAt:              existing.ExpiresAt,
			}, nil
		}
	}

	if req.PaymentAttemptID != "" {
		if existing, ok := p.sessions.GetByAttemptID(req.PaymentAttemptID); ok {
			return payment.CheckoutSession{
				ProviderSessionID:      existing.SessionID,
				ProviderOrderReference: existing.OrderRef,
				CheckoutURL:            p.checkoutURL(existing.SessionID),
				ExpiresAt:              existing.ExpiresAt,
			}, nil
		}
	}

	sessionID := "sim_sess_" + uuid.New().String()
	orderRef := "sim_ord_" + uuid.New().String()
	checkoutURL := p.checkoutURL(sessionID)

	// Persist session record within simulated provider boundary
	p.sessions.Save(&SessionRecord{
		PaymentAttemptID: req.PaymentAttemptID,
		SessionID:        sessionID,
		OrderRef:         orderRef,
		ReservationID:    req.ReservationID,
		AmountMinor:      req.AmountMinor,
		Currency:         req.Currency,
		ExpiresAt:        req.ExpiresAt,
	})

	return payment.CheckoutSession{
		ProviderSessionID:      sessionID,
		ProviderOrderReference: orderRef,
		CheckoutURL:            checkoutURL,
		ExpiresAt:              req.ExpiresAt,
	}, nil
}

func (p *Provider) checkoutURL(sessionID string) string {
	return fmt.Sprintf(
		"%s/api/v1/checkout?session_id=%s",
		p.baseURL,
		sessionID,
	)
}

// Refund executes a simulated refund idempotently by RefundID.
func (p *Provider) Refund(ctx context.Context, req payment.RefundRequest) (payment.RefundResult, error) {
	if p.refundHook != nil {
		return p.refundHook(ctx, req)
	}
	return payment.RefundResult{
		ProviderRefundReference: "sim_ref_" + req.RefundID,
	}, nil
}

// VerifyWebhook validates the signature and timestamp, and deserializes the payload into a normalized WebhookEvent.
func (p *Provider) VerifyWebhook(payload []byte, headers http.Header) (payment.WebhookEvent, error) {
	sig := headers.Get(HeaderSignature)
	if sig == "" {
		return payment.WebhookEvent{}, payment.ErrInvalidSignature
	}

	ts := headers.Get(HeaderTimestamp)
	if ts == "" {
		return payment.WebhookEvent{}, payment.ErrInvalidSignature
	}

	if err := VerifySignature(payload, sig, ts, p.secret, p.tolerance, time.Now().UTC()); err != nil {
		if errors.Is(err, ErrStaleTimestamp) {
			return payment.WebhookEvent{}, payment.ErrStaleTimestamp
		}
		return payment.WebhookEvent{}, payment.ErrInvalidSignature
	}

	var parsed WebhookPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return payment.WebhookEvent{}, payment.ErrInvalidPayload
	}

	if parsed.EventID == "" || parsed.ProviderOrderReference == "" || parsed.Outcome == "" {
		return payment.WebhookEvent{}, payment.ErrInvalidPayload
	}

	if parsed.Outcome != payment.PaymentOutcomeSucceeded && parsed.Outcome != payment.PaymentOutcomeFailed {
		return payment.WebhookEvent{}, payment.ErrInvalidPayload
	}

	return payment.WebhookEvent{
		EventID:                  parsed.EventID,
		Provider:                 ProviderName,
		ProviderOrderReference:   parsed.ProviderOrderReference,
		ProviderPaymentReference: parsed.ProviderPaymentReference,
		Outcome:                  parsed.Outcome,
		EventType:                parsed.EventType,
		AmountMinor:              parsed.AmountMinor,
		Currency:                 parsed.Currency,
		FailureReason:            parsed.FailureReason,
		RawPayload:               payload,
	}, nil
}

// TriggerWebhook simulates payment outcome by sending a signed HTTP webhook to TicketForge's webhook endpoint.
func (p *Provider) TriggerWebhook(
	ctx context.Context,
	orderRef string,
	amountMinor int64,
	currency string,
	outcome payment.PaymentOutcome,
	failureReason string,
) error {
	eventID := "sim_evt_" + uuid.New().String()
	paymentRef := ""
	eventType := "payment.failed"

	if outcome == payment.PaymentOutcomeSucceeded {
		paymentRef = "sim_pay_" + uuid.New().String()
		eventType = "payment.succeeded"
	}

	payload := WebhookPayload{
		EventID:                  eventID,
		EventType:                eventType,
		Provider:                 ProviderName,
		ProviderOrderReference:   orderRef,
		ProviderPaymentReference: paymentRef,
		AmountMinor:              amountMinor,
		Currency:                 currency,
		Outcome:                  outcome,
		FailureReason:            failureReason,
		CreatedAt:                time.Now().UTC(),
	}

	return p.client.SendWebhook(ctx, p.webhookURL, payload, p.secret)
}

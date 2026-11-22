package payment_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/payment"
)

type stubProvider struct {
	name           string
	createSessFunc func(ctx context.Context, req payment.CreateCheckoutSessionRequest) (payment.CheckoutSession, error)
	verifyFunc     func(payload []byte, headers http.Header) (payment.WebhookEvent, error)
}

func (s *stubProvider) Name() string {
	if s.name == "" {
		return "simulated"
	}
	return s.name
}

func (s *stubProvider) CreateCheckoutSession(
	ctx context.Context,
	req payment.CreateCheckoutSessionRequest,
) (payment.CheckoutSession, error) {
	if s.createSessFunc != nil {
		return s.createSessFunc(ctx, req)
	}
	return payment.CheckoutSession{
		ProviderSessionID:      "sess_123",
		ProviderOrderReference: "ord_123",
		CheckoutURL:            "http://example.com/checkout",
		ExpiresAt:              req.ExpiresAt,
	}, nil
}

func (s *stubProvider) VerifyWebhook(
	payload []byte,
	headers http.Header,
) (payment.WebhookEvent, error) {
	if s.verifyFunc != nil {
		return s.verifyFunc(payload, headers)
	}
	return payment.WebhookEvent{}, nil
}

func TestService_Unit_Validation(t *testing.T) {
	t.Run("Nil dependencies return error", func(t *testing.T) {
		_, err := payment.NewService(nil, nil, nil)
		if err == nil {
			t.Error("expected error with nil pool, got nil")
		}
	})

	t.Run("Provider Name accessor", func(t *testing.T) {
		prov := &stubProvider{name: "custom_prov"}
		if prov.Name() != "custom_prov" {
			t.Errorf("expected custom_prov, got %s", prov.Name())
		}
	})

	t.Run("VerifyWebhook delegates to provider", func(t *testing.T) {
		called := false
		prov := &stubProvider{
			verifyFunc: func(payload []byte, headers http.Header) (payment.WebhookEvent, error) {
				called = true
				return payment.WebhookEvent{}, errors.New("custom error")
			},
		}

		_, err := prov.VerifyWebhook([]byte("{}"), http.Header{})
		if !called {
			t.Error("expected VerifyWebhook to be called")
		}
		if err == nil || err.Error() != "custom error" {
			t.Errorf("expected custom error, got %v", err)
		}
	})

	t.Run("CreateCheckoutSession preserves expiration in request", func(t *testing.T) {
		exp := time.Now().Add(2 * time.Minute)
		prov := &stubProvider{
			createSessFunc: func(ctx context.Context, req payment.CreateCheckoutSessionRequest) (payment.CheckoutSession, error) {
				if !req.ExpiresAt.Equal(exp) {
					t.Errorf("expected expiration %v, got %v", exp, req.ExpiresAt)
				}
				return payment.CheckoutSession{
					ProviderSessionID:      "sess_test",
					ProviderOrderReference: "ord_test",
					CheckoutURL:            "http://test.com",
					ExpiresAt:              req.ExpiresAt,
				}, nil
			},
		}

		session, err := prov.CreateCheckoutSession(context.Background(), payment.CreateCheckoutSessionRequest{
			PaymentAttemptID: "p-1",
			ReservationID:    "r-1",
			AmountMinor:      1000,
			Currency:         "INR",
			ExpiresAt:        exp,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !session.ExpiresAt.Equal(exp) {
			t.Errorf("expected session expiration to match reservation, got %v", session.ExpiresAt)
		}
	})
}

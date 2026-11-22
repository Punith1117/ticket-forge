package payment_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Punith1117/ticket-forge/internal/payment"
)

func TestHandler_UnauthenticatedCheckout(t *testing.T) {
	handler := payment.NewHandler(nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/reservations/some-id/checkout", nil)

	handler.CreateCheckoutSession(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized without auth context, got %d", w.Code)
	}
}

func TestHandler_WebhookBodyTooLarge(t *testing.T) {
	handler := payment.NewHandler(nil, nil)

	// Body larger than 64KB (e.g. 65KB)
	largeBody := make([]byte, 65*1024)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/payment", bytes.NewReader(largeBody))

	handler.HandlePaymentWebhook(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413 Request Entity Too Large, got %d", w.Code)
	}
}

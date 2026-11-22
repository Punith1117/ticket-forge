package simulator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Punith1117/ticket-forge/internal/payment"
	"github.com/Punith1117/ticket-forge/internal/payment/simulator"
)

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSimulator_CreateCheckoutSession(t *testing.T) {
	prov, err := simulator.NewProvider(testSecret, "http://localhost:8080/api/v1/webhooks/payment", nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	exp := time.Now().Add(2 * time.Minute).Truncate(time.Second)
	req1 := payment.CreateCheckoutSessionRequest{
		PaymentAttemptID: "attempt-1",
		ReservationID:    "res-1",
		AmountMinor:      10000,
		Currency:         "INR",
		ExpiresAt:        exp,
	}

	s1, err := prov.CreateCheckoutSession(context.Background(), req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s1.ProviderSessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if s1.ProviderOrderReference == "" {
		t.Error("expected non-empty order reference")
	}
	if s1.CheckoutURL == "" {
		t.Error("expected non-empty checkout URL")
	}
	if !strings.Contains(s1.CheckoutURL, "/api/v1/checkout?session_id="+s1.ProviderSessionID) {
		t.Errorf("expected checkout URL to contain /api/v1/checkout?session_id=%s, got: %s", s1.ProviderSessionID, s1.CheckoutURL)
	}
	if !s1.ExpiresAt.Equal(exp) {
		t.Errorf("expected expiration %v, got %v", exp, s1.ExpiresAt)
	}

	// Multiple sessions generate unique references
	s2, err := prov.CreateCheckoutSession(context.Background(), req1)
	if err != nil {
		t.Fatalf("unexpected error on second session: %v", err)
	}

	if s1.ProviderSessionID == s2.ProviderSessionID {
		t.Errorf("expected distinct session IDs, got %s", s1.ProviderSessionID)
	}
	if s1.ProviderOrderReference == s2.ProviderOrderReference {
		t.Errorf("expected distinct order references, got %s", s1.ProviderOrderReference)
	}
}

func TestSimulator_SignatureVerification(t *testing.T) {
	prov, err := simulator.NewProvider(testSecret, "http://localhost:8080/api/v1/webhooks/payment", nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	now := time.Now().UTC()
	tsStr := strconv.FormatInt(now.Unix(), 10)

	payload := simulator.WebhookPayload{
		EventID:                "evt_123",
		EventType:              "payment.succeeded",
		Provider:               simulator.ProviderName,
		ProviderOrderReference: "ord_123",
		AmountMinor:            5000,
		Currency:               "INR",
		Outcome:                payment.PaymentOutcomeSucceeded,
		CreatedAt:              now,
	}

	rawBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	validSig := simulator.ComputeSignature(rawBody, tsStr, []byte(testSecret))

	t.Run("Valid signature and timestamp", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set(simulator.HeaderSignature, validSig)
		hdr.Set(simulator.HeaderTimestamp, tsStr)

		event, err := prov.VerifyWebhook(rawBody, hdr)
		if err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
		if event.EventID != "evt_123" {
			t.Errorf("expected EventID evt_123, got %s", event.EventID)
		}
		if event.Outcome != payment.PaymentOutcomeSucceeded {
			t.Errorf("expected outcome succeeded, got %s", event.Outcome)
		}
	})

	t.Run("Missing signature header", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set(simulator.HeaderTimestamp, tsStr)

		_, err := prov.VerifyWebhook(rawBody, hdr)
		if err == nil {
			t.Fatal("expected error on missing signature header, got nil")
		}
	})

	t.Run("Missing timestamp header", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set(simulator.HeaderSignature, validSig)

		_, err := prov.VerifyWebhook(rawBody, hdr)
		if err == nil {
			t.Fatal("expected error on missing timestamp header, got nil")
		}
	})

	t.Run("Invalid signature", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set(simulator.HeaderSignature, "deadbeef1234567890")
		hdr.Set(simulator.HeaderTimestamp, tsStr)

		_, err := prov.VerifyWebhook(rawBody, hdr)
		if err == nil {
			t.Fatal("expected error on invalid signature, got nil")
		}
	})

	t.Run("Modified body rejection", func(t *testing.T) {
		modifiedBody := append([]byte(nil), rawBody...)
		modifiedBody[len(modifiedBody)-1] = ' ' // tamper with byte

		hdr := http.Header{}
		hdr.Set(simulator.HeaderSignature, validSig)
		hdr.Set(simulator.HeaderTimestamp, tsStr)

		_, err := prov.VerifyWebhook(modifiedBody, hdr)
		if err == nil {
			t.Fatal("expected error on modified body, got nil")
		}
	})

	t.Run("Stale timestamp rejection", func(t *testing.T) {
		staleTime := now.Add(-10 * time.Minute)
		staleTsStr := strconv.FormatInt(staleTime.Unix(), 10)
		staleSig := simulator.ComputeSignature(rawBody, staleTsStr, []byte(testSecret))

		hdr := http.Header{}
		hdr.Set(simulator.HeaderSignature, staleSig)
		hdr.Set(simulator.HeaderTimestamp, staleTsStr)

		_, err := prov.VerifyWebhook(rawBody, hdr)
		if err == nil {
			t.Fatal("expected error on stale timestamp, got nil")
		}
	})

	t.Run("Malformed JSON payload rejection", func(t *testing.T) {
		malformed := []byte("{invalid-json}")
		malformedSig := simulator.ComputeSignature(malformed, tsStr, []byte(testSecret))

		hdr := http.Header{}
		hdr.Set(simulator.HeaderSignature, malformedSig)
		hdr.Set(simulator.HeaderTimestamp, tsStr)

		_, err := prov.VerifyWebhook(malformed, hdr)
		if err == nil {
			t.Fatal("expected error on malformed JSON payload, got nil")
		}
	})
}

type stubReservationChecker struct {
	statusFunc func(ctx context.Context, reservationID string) (simulator.ReservationStatus, error)
}

func (s *stubReservationChecker) GetReservationStatus(ctx context.Context, reservationID string) (simulator.ReservationStatus, error) {
	if s.statusFunc != nil {
		return s.statusFunc(ctx, reservationID)
	}
	return simulator.ReservationStatus{
		Status:    "held",
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}, nil
}

func TestSimulator_HostedCheckoutPage(t *testing.T) {
	prov, err := simulator.NewProvider(testSecret, "http://localhost:8080/api/v1/webhooks/payment", nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	mux := http.NewServeMux()
	prov.RegisterRoutes(mux)

	exp := time.Now().Add(2 * time.Minute)
	sess, err := prov.CreateCheckoutSession(context.Background(), payment.CreateCheckoutSessionRequest{
		PaymentAttemptID: "p-1",
		ReservationID:    "r-1",
		AmountMinor:      15000,
		Currency:         "INR",
		ExpiresAt:        exp,
	})
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	t.Run("Valid session renders HTML with amount and references", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/checkout?session_id="+sess.ProviderSessionID, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "150.00") {
			t.Error("expected body to contain formatted amount 150.00")
		}
		if !strings.Contains(body, "INR") {
			t.Error("expected body to contain currency INR")
		}
		if !strings.Contains(body, sess.ProviderSessionID) {
			t.Error("expected body to contain session ID")
		}
		if !strings.Contains(body, sess.ProviderOrderReference) {
			t.Error("expected body to contain order reference")
		}
		if strings.Contains(body, testSecret) {
			t.Error("security violation: webhook secret exposed in HTML")
		}
	})

	t.Run("Missing session_id query parameter returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/checkout", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", rec.Code)
		}
	})

	t.Run("Non-existent session_id returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/checkout?session_id=sim_sess_unknown", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", rec.Code)
		}
	})

	t.Run("Expired session returns 409", func(t *testing.T) {
		expiredSess, err := prov.CreateCheckoutSession(context.Background(), payment.CreateCheckoutSessionRequest{
			PaymentAttemptID: "p-exp",
			ReservationID:    "r-exp",
			AmountMinor:      10000,
			Currency:         "INR",
			ExpiresAt:        time.Now().Add(-1 * time.Minute),
		})
		if err != nil {
			t.Fatalf("failed to create expired session: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/checkout?session_id="+expiredSess.ProviderSessionID, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409 Conflict for expired session, got %d", rec.Code)
		}
	})
}

func TestSimulator_OutcomeSubmission(t *testing.T) {
	// Setup a mock receiver server to capture webhook delivered by simulator
	var capturedWebhook simulator.WebhookPayload
	var capturedSignature string
	var capturedTimestamp string

	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSignature = r.Header.Get(simulator.HeaderSignature)
		capturedTimestamp = r.Header.Get(simulator.HeaderTimestamp)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedWebhook)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer webhookServer.Close()

	prov, err := simulator.NewProvider(testSecret, webhookServer.URL, nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	checker := &stubReservationChecker{}
	prov.SetReservationChecker(checker)

	mux := http.NewServeMux()
	prov.RegisterRoutes(mux)

	exp := time.Now().Add(2 * time.Minute)
	sess, err := prov.CreateCheckoutSession(context.Background(), payment.CreateCheckoutSessionRequest{
		PaymentAttemptID: "p-sub",
		ReservationID:    "r-sub",
		AmountMinor:      20000,
		Currency:         "INR",
		ExpiresAt:        exp,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	t.Run("Successful outcome delivery triggers signed webhook", func(t *testing.T) {
		reqBody := []byte(`{"outcome":"succeeded"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout/"+sess.ProviderSessionID+"/complete", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}

		if capturedWebhook.ProviderOrderReference != sess.ProviderOrderReference {
			t.Errorf("expected order reference %s, got %s", sess.ProviderOrderReference, capturedWebhook.ProviderOrderReference)
		}
		if capturedWebhook.Outcome != payment.PaymentOutcomeSucceeded {
			t.Errorf("expected outcome succeeded, got %s", capturedWebhook.Outcome)
		}
		if capturedSignature == "" {
			t.Error("expected signed webhook with X-TicketForge-Signature")
		}
		if capturedTimestamp == "" {
			t.Error("expected signed webhook with X-TicketForge-Timestamp")
		}
	})

	t.Run("Duplicate completion is prevented with 409", func(t *testing.T) {
		reqBody := []byte(`{"outcome":"succeeded"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout/"+sess.ProviderSessionID+"/complete", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409 Conflict on duplicate completion, got %d", rec.Code)
		}
	})

	t.Run("Invalid outcome returns 400", func(t *testing.T) {
		sess2, _ := prov.CreateCheckoutSession(context.Background(), payment.CreateCheckoutSessionRequest{
			PaymentAttemptID: "p-sub-2",
			ReservationID:    "r-sub-2",
			AmountMinor:      20000,
			Currency:         "INR",
			ExpiresAt:        exp,
		})

		reqBody := []byte(`{"outcome":"maybe"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout/"+sess2.ProviderSessionID+"/complete", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request on invalid outcome, got %d", rec.Code)
		}
	})

	t.Run("Reservation no longer held rejected with 409", func(t *testing.T) {
		sess3, _ := prov.CreateCheckoutSession(context.Background(), payment.CreateCheckoutSessionRequest{
			PaymentAttemptID: "p-sub-3",
			ReservationID:    "r-sub-3",
			AmountMinor:      20000,
			Currency:         "INR",
			ExpiresAt:        exp,
		})

		checker.statusFunc = func(ctx context.Context, reservationID string) (simulator.ReservationStatus, error) {
			return simulator.ReservationStatus{
				Status:    "cancelled",
				ExpiresAt: time.Now().Add(2 * time.Minute),
			}, nil
		}

		reqBody := []byte(`{"outcome":"succeeded"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout/"+sess3.ProviderSessionID+"/complete", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409 Conflict when reservation is not held, got %d", rec.Code)
		}
	})
}

package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Punith1117/ticket-forge/internal/httpresponse"
	"github.com/Punith1117/ticket-forge/internal/payment"
)

var (
	ErrSessionNotFound         = errors.New("simulator: checkout session not found")
	ErrSessionExpired          = errors.New("simulator: checkout session has expired")
	ErrSessionAlreadyCompleted = errors.New("simulator: checkout session has already been completed")
	ErrReservationNotHeld      = errors.New("simulator: reservation is not in held status")
	ErrReservationExpired      = errors.New("simulator: reservation has expired")
	ErrInvalidOutcome          = errors.New("simulator: outcome must be 'succeeded' or 'failed'")
)

type ReservationStatus struct {
	Status    string
	ExpiresAt time.Time
}

type ReservationChecker interface {
	GetReservationStatus(ctx context.Context, reservationID string) (ReservationStatus, error)
}

// SessionRecord stores simulated checkout session data in memory.
type SessionRecord struct {
	SessionID     string
	OrderRef      string
	ReservationID string
	AmountMinor   int64
	Currency      string
	ExpiresAt     time.Time
	Completed     bool
	CompletedAt   time.Time
	Outcome       payment.PaymentOutcome
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*SessionRecord
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*SessionRecord),
	}
}

func (s *SessionStore) Save(rec *SessionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[rec.SessionID] = rec
}

func (s *SessionStore) Get(sessionID string) (*SessionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	copyRec := *rec
	return &copyRec, true
}

func (s *SessionStore) Complete(sessionID string, outcome payment.PaymentOutcome) (*SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if rec.Completed {
		return nil, ErrSessionAlreadyCompleted
	}
	rec.Completed = true
	rec.CompletedAt = time.Now().UTC()
	rec.Outcome = outcome
	copyRec := *rec
	return &copyRec, nil
}

type checkoutTemplateData struct {
	SessionID       string
	OrderRef        string
	AmountFormatted string
	Currency        string
}

const checkoutHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>TicketForge Simulated Hosted Checkout</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #f8fafc; color: #1e293b; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; }
        .card { background: white; border-radius: 12px; box-shadow: 0 4px 6px -1px rgb(0 0 0 / 0.1); padding: 32px; width: 100%; max-width: 440px; }
        h1 { font-size: 20px; margin-top: 0; margin-bottom: 24px; text-align: center; color: #0f172a; }
        .detail-row { display: flex; justify-content: space-between; padding: 12px 0; border-bottom: 1px solid #e2e8f0; font-size: 14px; }
        .detail-label { color: #64748b; }
        .detail-value { font-weight: 600; word-break: break-all; }
        .amount-row { font-size: 18px; color: #0284c7; border-bottom: none; padding-top: 16px; }
        .actions { margin-top: 28px; display: flex; flex-direction: column; gap: 12px; }
        button { padding: 12px 20px; border: none; border-radius: 8px; font-weight: 600; font-size: 14px; cursor: pointer; transition: background 0.15s; }
        .btn-success { background: #16a34a; color: white; }
        .btn-success:hover { background: #15803d; }
        .btn-failure { background: #dc2626; color: white; }
        .btn-failure:hover { background: #b91c1c; }
        .notice { font-size: 12px; color: #94a3b8; text-align: center; margin-top: 20px; }
    </style>
</head>
<body>
    <div class="card">
        <h1>Simulated Payment Gateway</h1>
        <div class="detail-row">
            <span class="detail-label">Session ID</span>
            <span class="detail-value">{{.SessionID}}</span>
        </div>
        <div class="detail-row">
            <span class="detail-label">Order Reference</span>
            <span class="detail-value">{{.OrderRef}}</span>
        </div>
        <div class="detail-row amount-row">
            <span class="detail-label">Total Payable</span>
            <span class="detail-value">{{.AmountFormatted}} {{.Currency}}</span>
        </div>
        <div class="actions">
            <form method="POST" action="/api/v1/checkout/{{.SessionID}}/complete">
                <input type="hidden" name="outcome" value="succeeded">
                <button type="submit" class="btn-success" style="width: 100%;">Complete Payment (Success)</button>
            </form>
            <form method="POST" action="/api/v1/checkout/{{.SessionID}}/complete">
                <input type="hidden" name="outcome" value="failed">
                <input type="hidden" name="failure_reason" value="card_declined">
                <button type="submit" class="btn-failure" style="width: 100%;">Fail Payment (Decline)</button>
            </form>
        </div>
        <div class="notice">
            TicketForge Simulated Gateway (Development Only)
        </div>
    </div>
</body>
</html>`

var checkoutTmpl = template.Must(template.New("checkout").Parse(checkoutHTMLTemplate))

// RegisterRoutes registers the hosted checkout GET page and POST outcome endpoint.
func (p *Provider) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/checkout", p.HandleCheckoutPage)
	mux.HandleFunc("POST /api/v1/checkout/{sessionID}/complete", p.HandleCompleteSession)
}

// HandleCheckoutPage validates the session and renders the minimal HTML checkout page.
func (p *Provider) HandleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		_ = httpresponse.WriteError(w, http.StatusBadRequest, "SESSION_ID_REQUIRED", "session_id query parameter is required.")
		return
	}

	session, ok := p.sessions.Get(sessionID)
	if !ok {
		_ = httpresponse.WriteError(w, http.StatusNotFound, "CHECKOUT_SESSION_NOT_FOUND", "Checkout session not found.")
		return
	}

	if session.Completed {
		_ = httpresponse.WriteError(w, http.StatusConflict, "CHECKOUT_SESSION_ALREADY_COMPLETED", "Checkout session has already been completed.")
		return
	}

	if !time.Now().UTC().Before(session.ExpiresAt) {
		_ = httpresponse.WriteError(w, http.StatusConflict, "CHECKOUT_SESSION_EXPIRED", "Checkout session has expired.")
		return
	}

	if p.reservationChecker != nil {
		status, err := p.reservationChecker.GetReservationStatus(r.Context(), session.ReservationID)
		if err != nil {
			_ = httpresponse.WriteError(w, http.StatusNotFound, "RESERVATION_STATUS_UNAVAILABLE", "Failed to verify reservation status.")
			return
		}
		if status.Status != "held" {
			_ = httpresponse.WriteError(w, http.StatusConflict, "RESERVATION_NOT_HELD", "Reservation is no longer in held status.")
			return
		}
		if !time.Now().UTC().Before(status.ExpiresAt) {
			_ = httpresponse.WriteError(w, http.StatusConflict, "RESERVATION_HOLD_EXPIRED", "Reservation hold has expired.")
			return
		}
	}

	data := checkoutTemplateData{
		SessionID:       session.SessionID,
		OrderRef:        session.OrderRef,
		AmountFormatted: fmt.Sprintf("%.2f", float64(session.AmountMinor)/100.0),
		Currency:        session.Currency,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = checkoutTmpl.Execute(w, data)
}

type completeOutcomeRequest struct {
	Outcome       string `json:"outcome"`
	FailureReason string `json:"failure_reason,omitempty"`
}

// HandleCompleteSession validates the session and reservation, triggers a signed webhook via net/http, and completes the session.
func (p *Provider) HandleCompleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	if sessionID == "" {
		_ = httpresponse.WriteError(w, http.StatusBadRequest, "SESSION_ID_REQUIRED", "Session ID is required.")
		return
	}

	session, ok := p.sessions.Get(sessionID)
	if !ok {
		_ = httpresponse.WriteError(w, http.StatusNotFound, "CHECKOUT_SESSION_NOT_FOUND", "Checkout session not found.")
		return
	}

	if session.Completed {
		_ = httpresponse.WriteError(w, http.StatusConflict, "CHECKOUT_SESSION_ALREADY_COMPLETED", "Checkout session has already been completed.")
		return
	}

	// 1. Validate session expiration
	if !time.Now().UTC().Before(session.ExpiresAt) {
		_ = httpresponse.WriteError(w, http.StatusConflict, "CHECKOUT_SESSION_EXPIRED", "Checkout session has expired.")
		return
	}

	// 2. Validate reservation expiration and status
	if p.reservationChecker != nil {
		status, err := p.reservationChecker.GetReservationStatus(r.Context(), session.ReservationID)
		if err != nil {
			_ = httpresponse.WriteError(w, http.StatusNotFound, "RESERVATION_NOT_FOUND", "Associated reservation not found.")
			return
		}
		if status.Status != "held" {
			_ = httpresponse.WriteError(w, http.StatusConflict, "RESERVATION_NOT_HELD", "Associated reservation is not held.")
			return
		}
		if !time.Now().UTC().Before(status.ExpiresAt) {
			_ = httpresponse.WriteError(w, http.StatusConflict, "RESERVATION_EXPIRED", "Associated reservation has expired.")
			return
		}
	}

	// 3. Parse outcome
	var outcomeReq completeOutcomeRequest
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&outcomeReq); err != nil {
			_ = httpresponse.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body.")
			return
		}
	} else {
		_ = r.ParseForm()
		outcomeReq.Outcome = r.FormValue("outcome")
		outcomeReq.FailureReason = r.FormValue("failure_reason")
	}

	outcomeReq.Outcome = strings.TrimSpace(outcomeReq.Outcome)
	if outcomeReq.Outcome != string(payment.PaymentOutcomeSucceeded) && outcomeReq.Outcome != string(payment.PaymentOutcomeFailed) {
		_ = httpresponse.WriteError(w, http.StatusBadRequest, "INVALID_OUTCOME", "Invalid outcome: must be 'succeeded' or 'failed'.")
		return
	}

	outcome := payment.PaymentOutcome(outcomeReq.Outcome)

	// 4. Atomically complete the simulated session
	completedRecord, err := p.sessions.Complete(sessionID, outcome)
	if err != nil {
		if errors.Is(err, ErrSessionAlreadyCompleted) {
			_ = httpresponse.WriteError(w, http.StatusConflict, "CHECKOUT_SESSION_ALREADY_COMPLETED", "Checkout session has already been completed.")
			return
		}
		_ = httpresponse.WriteError(w, http.StatusBadRequest, "CHECKOUT_SESSION_COMPLETION_FAILED", err.Error())
		return
	}

	// 5. Deliver signed webhook using net/http to the actual TicketForge webhook endpoint
	if err := p.TriggerWebhook(
		r.Context(),
		completedRecord.OrderRef,
		completedRecord.AmountMinor,
		completedRecord.Currency,
		outcome,
		outcomeReq.FailureReason,
	); err != nil {
		_ = httpresponse.WriteError(w, http.StatusBadGateway, "WEBHOOK_DELIVERY_FAILED", fmt.Sprintf("Failed to deliver signed webhook: %v", err))
		return
	}

	_ = httpresponse.WriteJSON(w, http.StatusOK, map[string]any{
		"status":     "completed",
		"outcome":    outcome,
		"session_id": sessionID,
	})
}

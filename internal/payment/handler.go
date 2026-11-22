package payment

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
}

func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return &Handler{
		service: service,
		logger:  logger,
	}
}

func (h *Handler) RegisterRoutes(
	mux *http.ServeMux,
	authHandler *auth.Handler,
) {
	// Authenticated checkout route
	mux.HandleFunc(
		"POST /api/v1/reservations/{reservationID}/checkout",
		authHandler.RequireAuth(h.CreateCheckoutSession),
	)

	// Public signature-verified payment webhook route
	mux.HandleFunc("POST /api/v1/webhooks/payment", h.HandlePaymentWebhook)
}

// CreateCheckoutSession handles checkout session creation for an authenticated customer's reservation.
func (h *Handler) CreateCheckoutSession(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	reservationID := r.PathValue("reservationID")
	if reservationID == "" {
		http.Error(w, "reservation ID is required", http.StatusBadRequest)
		return
	}

	resp, err := h.service.CreateCheckoutSession(r.Context(), authUser.UserID, reservationID)
	if err != nil {
		if h.handleCheckoutError(w, err) {
			return
		}

		h.logger.Error("failed to create checkout session",
			"user_id", authUser.UserID,
			"reservation_id", reservationID,
			"error", err,
		)
		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusCreated, resp)
}

// HandlePaymentWebhook processes incoming payment provider webhooks.
func (h *Handler) HandlePaymentWebhook(w http.ResponseWriter, r *http.Request) {
	// Max bounded request body: 64KB
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	if err := h.service.ProcessWebhook(r.Context(), body, r.Header); err != nil {
		if h.handleWebhookError(w, err) {
			return
		}

		h.logger.Error("failed to process payment webhook", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleCheckoutError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrReservationNotFound):
		http.Error(w, "reservation not found", http.StatusNotFound)
	case errors.Is(err, ErrReservationForbidden):
		http.Error(w, "reservation does not belong to customer", http.StatusForbidden)
	case errors.Is(err, ErrReservationNotHeld):
		http.Error(w, "reservation is not in held status", http.StatusConflict)
	case errors.Is(err, ErrReservationExpired):
		http.Error(w, "reservation has expired", http.StatusConflict)
	case errors.Is(err, ErrFreeReservationNotSupported):
		http.Error(w, "free reservations cannot use payment checkout", http.StatusBadRequest)
	case errors.Is(err, ErrProviderSessionFailed):
		http.Error(w, "failed to create checkout session with payment provider", http.StatusBadGateway)
	default:
		return false
	}
	return true
}

func (h *Handler) handleWebhookError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrInvalidSignature):
		http.Error(w, "invalid or missing webhook signature", http.StatusUnauthorized)
	case errors.Is(err, ErrStaleTimestamp):
		http.Error(w, "webhook timestamp outside acceptable window", http.StatusBadRequest)
	case errors.Is(err, ErrInvalidPayload):
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
	case errors.Is(err, ErrPaymentAttemptNotFound):
		http.Error(w, "payment attempt not found", http.StatusNotFound)
	case errors.Is(err, ErrAmountMismatch), errors.Is(err, ErrCurrencyMismatch):
		http.Error(w, "amount or currency mismatch", http.StatusBadRequest)
	case errors.Is(err, ErrInvalidStateTransition):
		http.Error(w, "invalid payment state transition", http.StatusConflict)
	default:
		return false
	}
	return true
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

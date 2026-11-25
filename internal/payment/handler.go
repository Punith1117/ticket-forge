package payment

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/httpresponse"
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
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
		return
	}

	reservationID := r.PathValue("reservationID")
	if reservationID == "" {
		h.writeError(w, http.StatusBadRequest, "RESERVATION_ID_REQUIRED", "Reservation ID is required.")
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
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusCreated, resp); err != nil {
		h.logger.Error("failed to write create checkout session response",
			"error", err,
		)
	}
}

// HandlePaymentWebhook processes incoming payment provider webhooks.
func (h *Handler) HandlePaymentWebhook(w http.ResponseWriter, r *http.Request) {
	// Max bounded request body: 64KB
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil {
		h.writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "Request body too large.")
		return
	}

	if err := h.service.ProcessWebhook(r.Context(), body, r.Header); err != nil {
		if h.handleWebhookError(w, err) {
			return
		}

		h.logger.Error("failed to process payment webhook", "error", err)
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "Internal server error.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"}); err != nil {
		h.logger.Error("failed to write payment webhook response",
			"error", err,
		)
	}
}

func (h *Handler) handleCheckoutError(w http.ResponseWriter, err error) bool {
	var (
		status  int
		code    string
		message string
	)

	switch {
	case errors.Is(err, ErrReservationNotFound):
		status = http.StatusNotFound
		code = "RESERVATION_NOT_FOUND"
		message = "Reservation not found."
	case errors.Is(err, ErrReservationForbidden):
		status = http.StatusForbidden
		code = "RESERVATION_FORBIDDEN"
		message = "Reservation does not belong to customer."
	case errors.Is(err, ErrReservationNotHeld):
		status = http.StatusConflict
		code = "RESERVATION_NOT_HELD"
		message = "Reservation is not in held status."
	case errors.Is(err, ErrReservationExpired):
		status = http.StatusConflict
		code = "RESERVATION_EXPIRED"
		message = "Reservation has expired."
	case errors.Is(err, ErrFreeReservationNotSupported):
		status = http.StatusBadRequest
		code = "FREE_RESERVATION_NOT_SUPPORTED"
		message = "Free reservations cannot use payment checkout."
	case errors.Is(err, ErrProviderSessionFailed):
		status = http.StatusBadGateway
		code = "PROVIDER_SESSION_FAILED"
		message = "Failed to create checkout session with payment provider."
	default:
		return false
	}

	h.writeError(w, status, code, message)
	return true
}

func (h *Handler) handleWebhookError(w http.ResponseWriter, err error) bool {
	var (
		status  int
		code    string
		message string
	)

	switch {
	case errors.Is(err, ErrInvalidSignature):
		status = http.StatusUnauthorized
		code = "INVALID_WEBHOOK_SIGNATURE"
		message = "Invalid or missing webhook signature."
	case errors.Is(err, ErrStaleTimestamp):
		status = http.StatusBadRequest
		code = "STALE_WEBHOOK_TIMESTAMP"
		message = "Webhook timestamp outside acceptable window."
	case errors.Is(err, ErrInvalidPayload):
		status = http.StatusBadRequest
		code = "INVALID_WEBHOOK_PAYLOAD"
		message = "Invalid webhook payload."
	case errors.Is(err, ErrPaymentAttemptNotFound):
		status = http.StatusNotFound
		code = "PAYMENT_ATTEMPT_NOT_FOUND"
		message = "Payment attempt not found."
	case errors.Is(err, ErrAmountMismatch), errors.Is(err, ErrCurrencyMismatch):
		status = http.StatusBadRequest
		code = "PAYMENT_MISMATCH"
		message = "Amount or currency mismatch."
	case errors.Is(err, ErrInvalidStateTransition):
		status = http.StatusConflict
		code = "INVALID_PAYMENT_STATE_TRANSITION"
		message = "Invalid payment state transition."
	default:
		return false
	}

	h.writeError(w, status, code, message)
	return true
}

func (h *Handler) writeError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
) {
	if err := httpresponse.WriteError(
		w,
		status,
		code,
		message,
	); err != nil {
		h.logger.Error("failed to write HTTP error response",
			"error", err,
		)
	}
}

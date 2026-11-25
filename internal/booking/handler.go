package booking

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/httpresponse"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
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
	mux.HandleFunc(
		"POST /api/v1/events/{eventID}/reservations",
		authHandler.RequireAuth(h.CreateReservation),
	)
}

func (h *Handler) CreateReservation(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
		return
	}

	eventID := r.PathValue("eventID")

	var req CreateReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body.")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		h.writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required.")
		return
	}

	response, err := h.service.CreateReservation(
		r.Context(),
		authUser.UserID,
		eventID,
		req,
		idempotencyKey,
	)
	if err != nil {
		if h.handleServiceError(w, err) {
			return
		}

		h.logger.Error("failed to create reservation",
			"user_id", authUser.UserID,
			"event_id", eventID,
			"ticket_category_id", req.TicketCategoryID,
			"error", err,
		)

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusCreated, response); err != nil {
		h.logger.Error("failed to write create reservation response",
			"error", err,
		)
	}
}

func (h *Handler) handleServiceError(w http.ResponseWriter, err error) bool {
	var (
		status  int
		code    string
		message string
	)

	switch {
	case errors.Is(err, ErrMissingTicketCategoryID):
		status = http.StatusBadRequest
		code = "MISSING_TICKET_CATEGORY_ID"
		message = "ticket_category_id is required."

	case errors.Is(err, ErrInvalidEventID),
		errors.Is(err, ErrInvalidTicketCategoryID),
		errors.Is(err, ErrInvalidQuantity):
		status = http.StatusBadRequest
		code = "INVALID_RESERVATION_REQUEST"
		message = err.Error()

	case errors.Is(err, idempotency.ErrInvalidRequest):
		status = http.StatusBadRequest
		code = "INVALID_IDEMPOTENCY_REQUEST"
		message = "Invalid idempotency request."

	case errors.Is(err, idempotency.ErrKeyReused):
		status = http.StatusConflict
		code = "IDEMPOTENCY_KEY_REUSED"
		message = "Idempotency key was already used with a different request."

	case errors.Is(err, idempotency.ErrIncompleteRecord):
		return false

	case errors.Is(err, ErrEventNotBookable):
		status = http.StatusConflict
		code = "EVENT_NOT_BOOKABLE"
		message = "Event is not currently accepting reservations."

	case errors.Is(err, ErrTicketCategoryUnavailable):
		status = http.StatusNotFound
		code = "TICKET_CATEGORY_UNAVAILABLE"
		message = "Ticket category is unavailable."

	case errors.Is(err, ErrInsufficientInventory):
		status = http.StatusConflict
		code = "INSUFFICIENT_INVENTORY"
		message = "Not enough tickets available."

	case errors.Is(err, ErrInventoryUnavailable):
		// This indicates an unexpected database/inventory consistency issue.
		return false

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

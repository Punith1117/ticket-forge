package booking

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
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
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	eventID := r.PathValue("eventID")

	var req CreateReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
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

		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) handleServiceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrMissingTicketCategoryID):
		http.Error(w, "ticket_category_id is required", http.StatusBadRequest)

	case errors.Is(err, ErrInvalidEventID),
		errors.Is(err, ErrInvalidTicketCategoryID),
		errors.Is(err, ErrInvalidQuantity):
		http.Error(w, err.Error(), http.StatusBadRequest)

	case errors.Is(err, idempotency.ErrInvalidRequest):
		http.Error(w, "invalid idempotency request", http.StatusBadRequest)

	case errors.Is(err, idempotency.ErrKeyReused):
		http.Error(
			w,
			"idempotency key was already used with a different request",
			http.StatusConflict,
		)

	case errors.Is(err, idempotency.ErrIncompleteRecord):
		return false

	case errors.Is(err, ErrEventNotBookable):
		http.Error(w, "event is not currently accepting reservations", http.StatusConflict)

	case errors.Is(err, ErrTicketCategoryUnavailable):
		http.Error(w, "ticket category is unavailable", http.StatusNotFound)

	case errors.Is(err, ErrInsufficientInventory):
		http.Error(w, "not enough tickets available", http.StatusConflict)

	case errors.Is(err, ErrInventoryUnavailable):
		// This indicates an unexpected database/inventory consistency issue.
		return false

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

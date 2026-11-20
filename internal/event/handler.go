package event

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/Punith1117/ticket-forge/internal/organizer"
)

type Handler struct {
	service          *Service
	organizerService *organizer.Service
	logger           *slog.Logger
}

func NewHandler(
	service *Service,
	organizerService *organizer.Service,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return &Handler{
		service:          service,
		organizerService: organizerService,
		logger:           logger,
	}
}

func (h *Handler) RegisterRoutes(
	mux *http.ServeMux,
	authHandler *auth.Handler,
) {
	mux.HandleFunc(
		"POST /api/v1/events",
		authHandler.RequireAuth(h.CreateEvent),
	)
}

func (h *Handler) getOrganizerProfileID(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
) (string, bool) {
	profile, err := h.organizerService.GetProfileByUserID(
		r.Context(),
		userID,
	)
	if err != nil {
		if errors.Is(err, organizer.ErrProfileNotFound) {
			http.Error(w, "organizer profile not found", http.StatusNotFound)
			return "", false
		}

		h.logger.Error("failed to retrieve organizer profile",
			"user_id", userID,
			"error", err,
		)

		http.Error(
			w,
			"an unexpected error occurred",
			http.StatusInternalServerError,
		)
		return "", false
	}

	return profile.ID.String(), true
}

func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	organizerProfileID, ok := h.getOrganizerProfileID(
		w,
		r,
		authUser.UserID,
	)
	if !ok {
		return
	}

	var req CreateEventRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Reject additional JSON values after the request object.
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(
			w,
			"Idempotency-Key header is required",
			http.StatusBadRequest,
		)
		return
	}

	response, err := h.service.Create(
		r.Context(),
		authUser.UserID,
		organizerProfileID,
		idempotencyKey,
		req,
	)
	if err != nil {
		if h.handleServiceError(w, err) {
			return
		}

		h.logger.Error("failed to create event",
			"user_id", authUser.UserID,
			"venue_booking_id", req.VenueBookingID,
			"error", err,
		)

		http.Error(
			w,
			"an unexpected error occurred",
			http.StatusInternalServerError,
		)
		return
	}

	h.writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) handleServiceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		http.Error(w, "invalid event request", http.StatusBadRequest)

	case errors.Is(err, ErrVenueBookingNotFound):
		http.Error(w, "venue booking not found", http.StatusNotFound)

	case errors.Is(err, ErrVenueBookingNotOwned):
		http.Error(w, "venue booking not found", http.StatusNotFound)

	case errors.Is(err, ErrVenueBookingUnavailable):
		http.Error(w, "venue booking is unavailable", http.StatusConflict)

	case errors.Is(err, ErrActiveEventAlreadyExists):
		http.Error(
			w,
			"an active event already exists for this venue booking",
			http.StatusConflict,
		)

	case errors.Is(err, ErrTicketCapacityExceeded):
		http.Error(
			w,
			"total ticket category capacity exceeds venue capacity",
			http.StatusBadRequest,
		)

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

package venuebooking

import (
	"encoding/json"
	"errors"
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
		"POST /api/v1/venue-bookings",
		authHandler.RequireAuth(h.CreateBooking),
	)

	mux.HandleFunc(
		"GET /api/v1/venue-bookings",
		authHandler.RequireAuth(h.ListBookings),
	)

	mux.HandleFunc(
		"GET /api/v1/venue-bookings/{id}",
		authHandler.RequireAuth(h.GetBooking),
	)

	mux.HandleFunc(
		"PATCH /api/v1/venue-bookings/{id}/cancel",
		authHandler.RequireAuth(h.CancelBooking),
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
		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return "", false
	}

	return profile.ID.String(), true
}

func (h *Handler) CreateBooking(w http.ResponseWriter, r *http.Request) {
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
	var req CreateVenueBookingRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
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

		h.logger.Error("failed to create venue booking",
			"user_id", authUser.UserID,
			"venue_id", req.VenueID,
			"error", err,
		)

		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) GetBooking(w http.ResponseWriter, r *http.Request) {
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
	bookingID := r.PathValue("id")

	response, err := h.service.Get(
		r.Context(),
		organizerProfileID,
		bookingID,
	)
	if err != nil {
		if h.handleServiceError(w, err) {
			return
		}

		h.logger.Error("failed to retrieve venue booking",
			"user_id", authUser.UserID,
			"booking_id", bookingID,
			"error", err,
		)

		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) ListBookings(w http.ResponseWriter, r *http.Request) {
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
	response, err := h.service.List(
		r.Context(),
		organizerProfileID,
	)
	if err != nil {
		h.logger.Error("failed to list venue bookings",
			"user_id", authUser.UserID,
			"error", err,
		)

		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) CancelBooking(w http.ResponseWriter, r *http.Request) {
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
	bookingID := r.PathValue("id")

	response, err := h.service.Cancel(
		r.Context(),
		organizerProfileID,
		bookingID,
	)
	if err != nil {
		if h.handleServiceError(w, err) {
			return
		}

		h.logger.Error("failed to cancel venue booking",
			"user_id", authUser.UserID,
			"booking_id", bookingID,
			"error", err,
		)

		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleServiceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrOrganizerNotFound):
		http.Error(w, "organizer profile not found", http.StatusNotFound)

	case errors.Is(err, ErrVenueNotFound):
		http.Error(w, "venue not found", http.StatusNotFound)

	case errors.Is(err, ErrVenueUnavailable):
		http.Error(w, "venue is unavailable", http.StatusConflict)

	case errors.Is(err, ErrVenueBookingNotFound):
		http.Error(w, "venue booking not found", http.StatusNotFound)

	case errors.Is(err, ErrVenueBookingCancelled):
		http.Error(w, "venue booking is already cancelled", http.StatusConflict)

	case errors.Is(err, ErrVenueBookingHasActiveEvent):
		http.Error(w, "venue booking has an active event", http.StatusConflict)

	case errors.Is(err, ErrVenueBookingLimitReached):
		http.Error(w, "organizer booking limit reached", http.StatusConflict)

	case errors.Is(err, ErrVenueBookingOverlap):
		http.Error(w, "venue is already booked for the selected time", http.StatusConflict)

	case errors.Is(err, ErrInvalidSchedule),
		errors.Is(err, ErrInvalidDuration),
		errors.Is(err, ErrStartTimeTooSoon),
		errors.Is(err, ErrStartTimeTooFar):
		http.Error(w, err.Error(), http.StatusBadRequest)

	case errors.Is(err, idempotency.ErrInvalidRequest):
		http.Error(w, "invalid idempotency request", http.StatusBadRequest)

	case errors.Is(err, idempotency.ErrKeyReused):
		http.Error(w, "idempotency key was already used with a different request", http.StatusConflict)

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

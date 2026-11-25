package venuebooking

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/httpresponse"
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
			h.writeError(w, http.StatusNotFound, "ORGANIZER_PROFILE_NOT_FOUND", "Organizer profile not found.")
			return "", false
		}

		h.logger.Error("failed to retrieve organizer profile",
			"user_id", userID,
			"error", err,
		)
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return "", false
	}

	return profile.ID.String(), true
}

func (h *Handler) CreateBooking(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
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
		h.writeError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid request body.")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		h.writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required.")
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

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusCreated, response); err != nil {
		h.logger.Error("failed to write create venue booking response",
			"error", err,
		)
	}
}

func (h *Handler) GetBooking(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
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

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write get venue booking response",
			"error", err,
		)
	}
}

func (h *Handler) ListBookings(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
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

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write list venue bookings response",
			"error", err,
		)
	}
}

func (h *Handler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
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

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	if err := httpresponse.WriteJSON(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write cancel venue booking response",
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
	case errors.Is(err, ErrOrganizerNotFound):
		status = http.StatusNotFound
		code = "ORGANIZER_PROFILE_NOT_FOUND"
		message = "Organizer profile not found."

	case errors.Is(err, ErrVenueNotFound):
		status = http.StatusNotFound
		code = "VENUE_NOT_FOUND"
		message = "Venue not found."

	case errors.Is(err, ErrVenueUnavailable):
		status = http.StatusConflict
		code = "VENUE_UNAVAILABLE"
		message = "Venue is unavailable."

	case errors.Is(err, ErrVenueBookingNotFound):
		status = http.StatusNotFound
		code = "VENUE_BOOKING_NOT_FOUND"
		message = "Venue booking not found."

	case errors.Is(err, ErrVenueBookingCancelled):
		status = http.StatusConflict
		code = "VENUE_BOOKING_CANCELLED"
		message = "Venue booking is already cancelled."

	case errors.Is(err, ErrVenueBookingHasActiveEvent):
		status = http.StatusConflict
		code = "VENUE_BOOKING_HAS_ACTIVE_EVENT"
		message = "Venue booking has an active event."

	case errors.Is(err, ErrVenueBookingLimitReached):
		status = http.StatusConflict
		code = "VENUE_BOOKING_LIMIT_REACHED"
		message = "Organizer booking limit reached."

	case errors.Is(err, ErrVenueBookingOverlap):
		status = http.StatusConflict
		code = "VENUE_BOOKING_OVERLAP"
		message = "Venue is already booked for the selected time."

	case errors.Is(err, ErrInvalidSchedule),
		errors.Is(err, ErrInvalidDuration),
		errors.Is(err, ErrStartTimeTooSoon),
		errors.Is(err, ErrStartTimeTooFar):
		status = http.StatusBadRequest
		code = "INVALID_VENUE_BOOKING_REQUEST"
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

package event

import (
	"encoding/json"
	"errors"
	"io"
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
		"POST /api/v1/events",
		authHandler.RequireAuth(h.CreateEvent),
	)
	mux.HandleFunc(
		"PATCH /api/v1/events/{id}/booking-status",
		authHandler.RequireAuth(h.UpdateBookingStatus),
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
			h.writeError(
				w,
				http.StatusNotFound,
				"ORGANIZER_PROFILE_NOT_FOUND",
				"Organizer profile not found.",
			)
			return "", false
		}

		h.logger.Error("failed to retrieve organizer profile",
			"user_id", userID,
			"error", err,
		)

		h.writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_SERVER_ERROR",
			"An unexpected error occurred.",
		)
		return "", false
	}

	return profile.ID.String(), true
}

func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(
			w,
			http.StatusUnauthorized,
			"UNAUTHORIZED",
			"Authentication required.",
		)
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
		h.writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Invalid request body.",
		)
		return
	}

	// Reject additional JSON values after the request object.
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		h.writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Invalid request body.",
		)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		h.writeError(
			w,
			http.StatusBadRequest,
			"IDEMPOTENCY_KEY_REQUIRED",
			"Idempotency-Key header is required.",
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

		h.writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_SERVER_ERROR",
			"An unexpected error occurred.",
		)
		return
	}

	if err := httpresponse.WriteJSON(
		w,
		http.StatusCreated,
		response,
	); err != nil {
		h.logger.Error("failed to write create event response",
			"error", err,
		)
	}
}

func (h *Handler) UpdateBookingStatus(
	w http.ResponseWriter,
	r *http.Request,
) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(
			w,
			http.StatusUnauthorized,
			"UNAUTHORIZED",
			"Authentication required.",
		)
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

	var req UpdateBookingStatusRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		h.writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Invalid request body.",
		)
		return
	}

	// Reject additional JSON values after the request object.
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		h.writeError(
			w,
			http.StatusBadRequest,
			"INVALID_JSON",
			"Invalid request body.",
		)
		return
	}

	eventID := r.PathValue("id")

	response, err := h.service.UpdateBookingStatus(
		r.Context(),
		organizerProfileID,
		eventID,
		req,
	)
	if err != nil {
		if h.handleServiceError(w, err) {
			return
		}

		h.logger.Error("failed to update event booking status",
			"user_id", authUser.UserID,
			"event_id", eventID,
			"booking_status", req.BookingStatus,
			"error", err,
		)

		h.writeError(
			w,
			http.StatusInternalServerError,
			"INTERNAL_SERVER_ERROR",
			"An unexpected error occurred.",
		)
		return
	}

	if err := httpresponse.WriteJSON(
		w,
		http.StatusOK,
		response,
	); err != nil {
		h.logger.Error("failed to write booking status response",
			"error", err,
		)
	}
}

func (h *Handler) handleServiceError(
	w http.ResponseWriter,
	err error,
) bool {
	var (
		status  int
		code    string
		message string
	)

	switch {
	case errors.Is(err, ErrInvalidRequest):
		status = http.StatusBadRequest
		code = "INVALID_EVENT_REQUEST"
		message = "Invalid event request."

	case errors.Is(err, ErrVenueBookingNotFound),
		errors.Is(err, ErrVenueBookingNotOwned):
		status = http.StatusNotFound
		code = "VENUE_BOOKING_NOT_FOUND"
		message = "Venue booking not found."

	case errors.Is(err, ErrVenueBookingUnavailable):
		status = http.StatusConflict
		code = "VENUE_BOOKING_UNAVAILABLE"
		message = "Venue booking is unavailable."

	case errors.Is(err, ErrActiveEventAlreadyExists):
		status = http.StatusConflict
		code = "ACTIVE_EVENT_ALREADY_EXISTS"
		message = "An active event already exists for this venue booking."

	case errors.Is(err, ErrTicketCapacityExceeded):
		status = http.StatusBadRequest
		code = "TICKET_CAPACITY_EXCEEDED"
		message = "Total ticket category capacity exceeds venue capacity."

	case errors.Is(err, ErrEventNotFound):
		status = http.StatusNotFound
		code = "EVENT_NOT_FOUND"
		message = "Event not found."

	case errors.Is(err, ErrEventNotActive):
		status = http.StatusConflict
		code = "EVENT_NOT_ACTIVE"
		message = "Cancelled events cannot be reopened."

	case errors.Is(err, ErrInvalidBookingStatus):
		status = http.StatusBadRequest
		code = "INVALID_BOOKING_STATUS"
		message = "Booking status must be either 'open' or 'paused'."

	case errors.Is(err, idempotency.ErrInvalidRequest):
		status = http.StatusBadRequest
		code = "INVALID_IDEMPOTENCY_REQUEST"
		message = "Invalid idempotency request."

	case errors.Is(err, idempotency.ErrKeyReused):
		status = http.StatusConflict
		code = "IDEMPOTENCY_KEY_REUSED"
		message = "Idempotency key was already used with a different request."

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

package organizer

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/httpresponse"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
		"POST /api/v1/organizer-profiles",
		authHandler.RequireAuth(h.CreateProfile),
	)
}

func (h *Handler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	authUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
		return
	}

	profile, created, err := h.service.CreateOrGetProfile(
		r.Context(),
		authUser.UserID,
	)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			h.writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "User not found.")
			return
		}

		h.logger.Error("failed to create organizer profile",
			"user_id", authUser.UserID,
			"error", err,
		)
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
		return
	}

	status := http.StatusOK
	message := "Organizer profile already exists."

	if created {
		status = http.StatusCreated
		message = "Organizer profile created successfully."
	}

	if err := httpresponse.WriteJSON(w, status, CreateProfileResponse{
		Message: message,
		Profile: toProfileResponse(profile),
	}); err != nil {
		h.logger.Error("failed to write organizer profile response",
			"error", err,
		)
	}
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

func toProfileResponse(profile sqlc.OrganizerProfile) ProfileResponse {
	return ProfileResponse{
		ID:          uuidString(profile.ID),
		DisplayName: profile.DisplayName,
		Status:      profile.Status,
		CreatedAt:   profile.CreatedAt.Time,
	}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}

	id, err := uuid.FromBytes(u.Bytes[:])
	if err != nil {
		return ""
	}

	return id.String()
}

package organizer

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/auth"
	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
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
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	profile, created, err := h.service.CreateOrGetProfile(
		r.Context(),
		authUser.UserID,
	)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}

		h.logger.Error("failed to create organizer profile",
			"user_id", authUser.UserID,
			"error", err,
		)
		http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
		return
	}

	status := http.StatusOK
	message := "Organizer profile already exists."

	if created {
		status = http.StatusCreated
		message = "Organizer profile created successfully."
	}

	h.writeJSON(w, status, CreateProfileResponse{
		Message: message,
		Profile: toProfileResponse(profile),
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
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

package auth

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

const maxRequestBodySize = 64 * 1024 // 64 KB

// Handler processes HTTP requests for user authentication and registration.
type Handler struct {
	service       *Service
	logger        *slog.Logger
	secureCookies bool
}

// NewHandler constructs a new auth Handler.
func NewHandler(service *Service, logger *slog.Logger, secureCookies bool) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		service:       service,
		logger:        logger,
		secureCookies: secureCookies,
	}
}

// RegisterRoutes mounts auth endpoints onto the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.HandleRegister)
	mux.HandleFunc("POST /api/v1/auth/register/complete", h.HandleRegisterComplete)
	mux.HandleFunc("POST /api/v1/auth/login", h.HandleLogin)
}

// ErrorResponse defines the standard JSON error schema.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, errCode, message string) {
	h.writeJSON(w, status, ErrorResponse{
		Error:   errCode,
		Message: message,
	})
}

// decodeJSON reads and strictly decodes a JSON request body with size limiting.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return err
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}

	return nil
}

// HandleRegister handles POST /api/v1/auth/register.
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "invalid or malformed JSON payload")
		return
	}

	err := h.service.RequestRegistration(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, ErrInvalidEmail) {
			h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if errors.Is(err, ErrCooldownActive) {
			h.writeError(w, http.StatusTooManyRequests, "too_many_requests", err.Error())
			return
		}
		h.logger.Error("failed to process registration request", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}

	h.writeJSON(w, http.StatusAccepted, RegisterResponse{
		Message: "If this email address is eligible for registration, a verification link has been sent.",
	})
}

// HandleRegisterComplete handles POST /api/v1/auth/register/complete.
func (h *Handler) HandleRegisterComplete(w http.ResponseWriter, r *http.Request) {
	var req RegisterCompleteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "invalid or malformed JSON payload")
		return
	}

	user, err := h.service.CompleteRegistration(r.Context(), req.Token, req.Password, req.DisplayName)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidToken):
			h.writeError(w, http.StatusBadRequest, "invalid_token", err.Error())
		case errors.Is(err, ErrTokenExpired):
			h.writeError(w, http.StatusBadRequest, "token_expired", err.Error())
		case errors.Is(err, ErrTokenAlreadyConsumed):
			h.writeError(w, http.StatusBadRequest, "token_already_used", err.Error())
		case errors.Is(err, ErrInvalidPassword):
			h.writeError(w, http.StatusBadRequest, "invalid_password", err.Error())
		case errors.Is(err, ErrEmailAlreadyRegistered):
			h.writeError(w, http.StatusConflict, "conflict", err.Error())
		default:
			h.logger.Error("failed to complete registration", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		}
		return
	}

	h.writeJSON(w, http.StatusCreated, RegisterCompleteResponse{
		Message: "Registration completed successfully.",
		User:    *user,
	})
}

// HandleLogin handles POST /api/v1/auth/login.
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "invalid or malformed JSON payload")
		return
	}

	if req.Email == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "email is required")
		return
	}
	if req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "password is required")
		return
	}

	rawToken, info, err := h.service.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			h.writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		h.logger.Error("login failed with internal error", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}

	// Issue session cookie. Only reached after successful persistence.
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  info.AbsoluteExpiresAt,
		MaxAge:   int(SessionAbsoluteLifetime.Seconds()),
	})

	h.writeJSON(w, http.StatusOK, LoginResponse{
		Message: "Login successful.",
	})
}

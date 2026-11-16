package auth

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Punith1117/ticket-forge/internal/ratelimit"
	"github.com/Punith1117/ticket-forge/internal/security"
)

const maxRequestBodySize = 64 * 1024 // 64 KB

// Option configures a Handler.
type Option func(*Handler)

// WithRateLimiter configures the rate limiter and key deriver for the handler.
func WithRateLimiter(limiter ratelimit.Limiter, keyDeriver *ratelimit.KeyDeriver) Option {
	return func(h *Handler) {
		h.limiter = limiter
		h.keyDeriver = keyDeriver
	}
}

// WithIPExtractor configures the client IP extractor for the handler.
func WithIPExtractor(extractor *IPExtractor) Option {
	return func(h *Handler) {
		h.ipExtractor = extractor
	}
}

// Handler processes HTTP requests for user authentication and registration.
type Handler struct {
	service       *Service
	logger        *slog.Logger
	secureCookies bool
	limiter       ratelimit.Limiter
	keyDeriver    *ratelimit.KeyDeriver
	ipExtractor   *IPExtractor
}

// NewHandler constructs a new auth Handler.
func NewHandler(service *Service, logger *slog.Logger, secureCookies bool, opts ...Option) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		service:       service,
		logger:        logger,
		secureCookies: secureCookies,
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.ipExtractor == nil {
		h.ipExtractor, _ = NewIPExtractor(nil)
	}
	return h
}

// RegisterRoutes mounts auth endpoints onto the provided ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.HandleRegister)
	mux.HandleFunc("POST /api/v1/auth/register/complete", h.HandleRegisterComplete)
	mux.HandleFunc("POST /api/v1/auth/login", h.HandleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", h.RequireAuth(h.HandleLogout))
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

// writeRateLimitError returns HTTP 429 Too Many Requests with a Retry-After header.
func (h *Handler) writeRateLimitError(w http.ResponseWriter, retryAfter time.Duration) {
	retrySeconds := int(math.Ceil(retryAfter.Seconds()))
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
	h.writeError(w, http.StatusTooManyRequests, "too_many_requests", "rate limit exceeded")
}

func (h *Handler) clientIP(r *http.Request) string {
	if h.ipExtractor != nil {
		return h.ipExtractor.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
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

	normEmail, err := NormalizeEmail(req.Email)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Rate limiting protections: evaluate both IP and email limits before generating tokens or challenge
	if h.limiter != nil && h.keyDeriver != nil {
		// 1. IP-based limit
		clientIP := h.clientIP(r)
		ipKey, err := h.keyDeriver.DeriveKey(PolicyRegisterIP.ID, ratelimit.DomainIP, clientIP)
		if err != nil {
			h.logger.Error("failed to derive register IP key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}

		ipRes, err := h.limiter.Allow(r.Context(), ipKey, PolicyRegisterIP)
		if err != nil {
			// Fail closed on database / infrastructure errors
			h.logger.Error("register IP rate limit evaluation failed", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}
		if !ipRes.Allowed {
			h.writeRateLimitError(w, ipRes.RetryAfter)
			return
		}

		// 2. Normalized email limit
		emailKey, err := h.keyDeriver.DeriveKey(PolicyRegisterEmail.ID, ratelimit.DomainEmail, normEmail)
		if err != nil {
			h.logger.Error("failed to derive register email key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}

		emailRes, err := h.limiter.Allow(r.Context(), emailKey, PolicyRegisterEmail)
		if err != nil {
			// Fail closed on database / infrastructure errors
			h.logger.Error("register email rate limit evaluation failed", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}
		if !emailRes.Allowed {
			h.writeRateLimitError(w, emailRes.RetryAfter)
			return
		}
	}

	// 3. Initiate challenge (enforces 60-second cooldown, user existence check, challenge + outbox insertion)
	err = h.service.RequestRegistration(r.Context(), normEmail)
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

	rawToken := strings.TrimSpace(req.Token)
	if rawToken == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_token", ErrInvalidToken.Error())
		return
	}

	// Rate limiting protections: evaluate IP and token identifier limits before Argon2id hashing or DB update
	if h.limiter != nil && h.keyDeriver != nil {
		// 1. IP-based limit
		clientIP := h.clientIP(r)
		ipKey, err := h.keyDeriver.DeriveKey(PolicyRegisterCompleteIP.ID, ratelimit.DomainIP, clientIP)
		if err != nil {
			h.logger.Error("failed to derive register complete IP key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}

		ipRes, err := h.limiter.Allow(r.Context(), ipKey, PolicyRegisterCompleteIP)
		if err != nil {
			// Fail closed on database / infrastructure errors
			h.logger.Error("register complete IP rate limit evaluation failed", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}
		if !ipRes.Allowed {
			h.writeRateLimitError(w, ipRes.RetryAfter)
			return
		}

		// 2. Token identifier limit (SHA-256 hash input to key deriver; raw token is never persisted)
		tokenHash := security.HashToken(rawToken)
		tokenKey, err := h.keyDeriver.DeriveKey(PolicyRegisterCompleteToken.ID, ratelimit.DomainToken, tokenHash)
		if err != nil {
			h.logger.Error("failed to derive register complete token key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}

		tokenRes, err := h.limiter.Allow(r.Context(), tokenKey, PolicyRegisterCompleteToken)
		if err != nil {
			// Fail closed on database / infrastructure errors
			h.logger.Error("register complete token rate limit evaluation failed", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}
		if !tokenRes.Allowed {
			h.writeRateLimitError(w, tokenRes.RetryAfter)
			return
		}
	}

	// 3. Complete registration
	user, err := h.service.CompleteRegistration(r.Context(), rawToken, req.Password, req.DisplayName)
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

	// 1. IP-based request limit (evaluated on EVERY login request before password verification)
	if h.limiter != nil && h.keyDeriver != nil {
		clientIP := h.clientIP(r)
		ipKey, err := h.keyDeriver.DeriveKey(PolicyLoginIP.ID, ratelimit.DomainIP, clientIP)
		if err != nil {
			h.logger.Error("failed to derive login IP key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}

		ipRes, err := h.limiter.Allow(r.Context(), ipKey, PolicyLoginIP)
		if err != nil {
			// Fail closed on database / infrastructure errors
			h.logger.Error("login IP rate limit evaluation failed", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			return
		}
		if !ipRes.Allowed {
			h.writeRateLimitError(w, ipRes.RetryAfter)
			return
		}
	}

	// 2. Perform authentication attempt
	rawToken, info, err := h.service.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			// Authentication failed. Evaluate email-based failed-attempt limit.
			if h.limiter != nil && h.keyDeriver != nil {
				normEmail, nErr := NormalizeEmail(req.Email)
				if nErr != nil {
					normEmail = strings.ToLower(strings.TrimSpace(req.Email))
				}
				emailKey, kErr := h.keyDeriver.DeriveKey(PolicyLoginEmailFailure.ID, ratelimit.DomainEmail, normEmail)
				if kErr != nil {
					h.logger.Error("failed to derive login email failure key", "error", kErr)
					h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
					return
				}

				failRes, fErr := h.limiter.Allow(r.Context(), emailKey, PolicyLoginEmailFailure)
				if fErr != nil {
					// Fail closed on database / infrastructure errors
					h.logger.Error("login email failure rate limit evaluation failed", "error", fErr)
					h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
					return
				}
				if !failRes.Allowed {
					// Bucket is depleted! Reject without consuming another failure token.
					h.writeRateLimitError(w, failRes.RetryAfter)
					return
				}
			}

			// Failure token was consumed within capacity; return generic credentials error
			h.writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}

		h.logger.Error("login failed with internal error", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}

	// 3. Successful authentication: issue session cookie (no failure token consumed)
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

// HandleLogout handles POST /api/v1/auth/logout.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	authUser, ok := UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if err := h.service.Logout(r.Context(), authUser.SessionID); err != nil {
		h.logger.Error("logout failed", slog.String("error", err.Error()))
		h.writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})

	h.writeJSON(w, http.StatusOK, struct {
		Message string `json:"message"`
	}{Message: "Logged out successfully."})
}

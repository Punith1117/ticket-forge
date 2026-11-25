package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Punith1117/ticket-forge/internal/security"
)

// contextKey is a private unexported type for context keys within this package.
// Using a named struct prevents collisions with keys from other packages.
type contextKey struct{}

// authenticatedUserKey is the singleton context key under which AuthenticatedUser is stored.
var authenticatedUserKey = contextKey{}

// AuthenticatedUser holds the validated identity of an authenticated user attached to a
// request context by the session middleware. It contains only stable identifiers and no
// mutable session state or raw credentials.
type AuthenticatedUser struct {
	UserID    string // stable UUID of the authenticated user
	SessionID string // UUID of the current session
}

// UserFromContext retrieves the authenticated user identity from the request context.
// Returns the AuthenticatedUser and true when the middleware has successfully validated
// a session, or the zero value and false otherwise.
// Downstream handlers must not proceed with business logic when ok is false.
func UserFromContext(ctx context.Context) (AuthenticatedUser, bool) {
	u, ok := ctx.Value(authenticatedUserKey).(AuthenticatedUser)
	return u, ok
}

// Middleware returns an http.Handler that authenticates incoming requests using the
// session cookie. It must wrap any handler that requires an authenticated user.
//
// On success:
//   - Attaches an AuthenticatedUser to the request context.
//   - Calls next.
//
// On session not found / revoked / idle-expired / absolute-expired:
//   - Returns 401 Unauthorized with a generic message.
//   - Does NOT call next.
//   - Does not reveal the specific reason for rejection.
//
// On database errors:
//   - Returns 500 Internal Server Error.
//   - Never silently treats a database outage as an invalid session.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			// http.ErrNoCookie or parse error — no session present.
			h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
			return
		}

		rawToken := cookie.Value
		if rawToken == "" {
			h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
			return
		}

		// Hash the raw token before any database interaction.
		// The raw token must never be logged or stored.
		tokenHash := security.HashToken(rawToken)

		session, err := h.service.ValidateSession(r.Context(), tokenHash)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				// Unknown, revoked, or expired — generic 401, no detail leaked.
				h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
				return
			}
			// Genuine database or internal error.
			// Return 500 — do not silently log users out on infrastructure failures.
			h.logger.Error("session validation failed", slog.String("error", err.Error()))
			h.writeError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred.")
			return
		}

		authUser := AuthenticatedUser{
			UserID:    uuidString(session.UserID),
			SessionID: uuidString(session.ID),
		}

		ctx := context.WithValue(r.Context(), authenticatedUserKey, authUser)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth wraps an http.HandlerFunc with session authentication middleware.
// Convenience adapter for use with http.ServeMux.HandleFunc.
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return h.Middleware(http.HandlerFunc(next)).ServeHTTP
}

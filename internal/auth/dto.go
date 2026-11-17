package auth

import "time"

// RegisterRequest holds the payload for initiating email registration.
type RegisterRequest struct {
	Email string `json:"email"`
}

// RegisterResponse represents the generic response returned on registration requests.
type RegisterResponse struct {
	Message string `json:"message"`
}

// RegisterCompleteRequest holds the payload for finalizing registration with a password.
type RegisterCompleteRequest struct {
	Token       string `json:"token"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
}

// UserResponse represents safe user details returned after successful registration.
type UserResponse struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// RegisterCompleteResponse holds the response payload on successful registration completion.
type RegisterCompleteResponse struct {
	Message string       `json:"message"`
	User    UserResponse `json:"user"`
}

// LoginRequest holds the payload for user login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse represents the response after a successful login.
type LoginResponse struct {
	Message string `json:"message"`
}

// PasswordResetRequest holds the payload for initiating a password reset.
type PasswordResetRequest struct {
	Email string `json:"email"`
}

// PasswordResetResponse represents the generic response returned on password reset request.
type PasswordResetResponse struct {
	Message string `json:"message"`
}

// PasswordResetCompleteRequest holds the payload for finalizing password reset.
type PasswordResetCompleteRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// PasswordResetCompleteResponse represents the response on successful password reset completion.
type PasswordResetCompleteResponse struct {
	Message string `json:"message"`
}

// SessionInfo holds details of a created session (for internal use and testing).
type SessionInfo struct {
	SessionID         string
	UserID            string
	AbsoluteExpiresAt time.Time
}

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

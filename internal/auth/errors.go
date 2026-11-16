package auth

import "errors"

var (
	// ErrInvalidEmail indicates that the email address fails formatting/syntax rules.
	ErrInvalidEmail = errors.New("invalid email address format")

	// ErrCooldownActive indicates that a registration challenge was requested too recently for this email.
	ErrCooldownActive = errors.New("please wait at least 60 seconds before requesting another verification email")

	// ErrInvalidToken indicates that no active challenge matches the provided token hash.
	ErrInvalidToken = errors.New("invalid verification token")

	// ErrTokenExpired indicates that the verification challenge has passed its 24-hour expiration.
	ErrTokenExpired = errors.New("verification token has expired")

	// ErrTokenAlreadyConsumed indicates that the verification challenge was already used to create an account.
	ErrTokenAlreadyConsumed = errors.New("verification token has already been used")

	// ErrInvalidPassword indicates that the password does not satisfy length requirements.
	ErrInvalidPassword = errors.New("password must be between 12 and 128 characters")

	// ErrEmailAlreadyRegistered indicates that an account with this normalized email address already exists.
	ErrEmailAlreadyRegistered = errors.New("an account with this email address already exists")

	// ErrInvalidCredentials is the generic error for failed authentication (email not found, wrong password, or unverified account).
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrTokenGenerationFailed indicates session token generation failed.
	ErrTokenGenerationFailed = errors.New("failed to generate session token")

	// ErrSessionNotFound indicates that no matching active session was found for the provided token.
	// Returned for unknown, revoked, idle-expired, and absolute-expired sessions.
	ErrSessionNotFound = errors.New("session not found or expired")

	// ErrLogoutFailed indicates that session revocation did not affect any rows.
	ErrLogoutFailed = errors.New("failed to revoke session")
)

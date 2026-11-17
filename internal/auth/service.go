package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/Punith1117/ticket-forge/internal/outbox"
	"github.com/Punith1117/ticket-forge/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultCooldownDuration is the required resend cooldown between registration challenges.
	DefaultCooldownDuration = 60 * time.Second

	// DefaultTokenExpiry is the lifetime of a registration verification token.
	DefaultTokenExpiry = 24 * time.Hour

	// DefaultPasswordResetTokenExpiry is the lifetime of a password reset token per ADR-013.
	DefaultPasswordResetTokenExpiry = 20 * time.Minute

	// SessionIdleTimeout is the idle session timeout per ADR-012.
	SessionIdleTimeout = 30 * time.Minute

	// SessionAbsoluteLifetime is the absolute session lifetime per ADR-012.
	SessionAbsoluteLifetime = 7 * 24 * time.Hour

	// SessionCookieName is the name of the HTTP cookie used to transport the session token.
	SessionCookieName = "session_token"
)

// dummyPasswordHash is a valid Argon2id hash pre-computed at startup for timing mitigation
// on unknown-email login attempts. It prevents obvious timing differences between
// "user not found" and "wrong password" paths.
var dummyPasswordHash string

func init() {
	h, err := security.HashPassword("dummy-password-for-timing-protection-12345!")
	if err != nil {
		// This should never fail; panic at startup is acceptable.
		panic("auth: failed to initialize dummy password hash: " + err.Error())
	}
	dummyPasswordHash = h
}

// Service provides user registration business operations.
type Service struct {
	pool        *pgxpool.Pool
	queries     *sqlc.Queries
	encrypter   *security.TokenEncrypter
	cooldown    time.Duration
	tokenExpiry time.Duration
	resetExpiry time.Duration
}

// NewService constructs a new auth Service.
func NewService(pool *pgxpool.Pool, encrypter *security.TokenEncrypter) (*Service, error) {
	if pool == nil {
		return nil, errors.New("auth: database pool cannot be nil")
	}
	if encrypter == nil {
		return nil, errors.New("auth: token encrypter cannot be nil")
	}

	return &Service{
		pool:        pool,
		queries:     sqlc.New(pool),
		encrypter:   encrypter,
		cooldown:    DefaultCooldownDuration,
		tokenExpiry: DefaultTokenExpiry,
		resetExpiry: DefaultPasswordResetTokenExpiry,
	}, nil
}

// SetCooldown sets the challenge resend cooldown duration (useful for testing).
func (s *Service) SetCooldown(d time.Duration) {
	s.cooldown = d
}

// SetTokenExpiry sets the token expiration lifetime (useful for testing).
func (s *Service) SetTokenExpiry(d time.Duration) {
	s.tokenExpiry = d
}

// SetPasswordResetTokenExpiry sets the password reset token expiration lifetime (useful for testing).
func (s *Service) SetPasswordResetTokenExpiry(d time.Duration) {
	s.resetExpiry = d
}

// NormalizeEmail cleans and validates an email address.
func NormalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "", ErrInvalidEmail
	}

	addr, err := mail.ParseAddress(normalized)
	if err != nil || addr.Address != normalized {
		return "", ErrInvalidEmail
	}

	parts := strings.Split(normalized, "@")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 || !strings.Contains(parts[1], ".") {
		return "", ErrInvalidEmail
	}

	return normalized, nil
}

// RequestRegistration initiates the email verification challenge for user registration.
// Operates inside an atomic database transaction and guarantees:
// - 60s cooldown enforcement via PostgreSQL advisory transaction locks.
// - Generic non-leaking responses regardless of existing accounts.
// - Atomically writes registration challenge and transactional outbox event.
func (s *Service) RequestRegistration(ctx context.Context, email string) error {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Serialize concurrent requests for the same email using transaction-scoped advisory locks.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('rvc_cooldown:' || $1::text))", normalizedEmail); err != nil {
		return fmt.Errorf("auth: failed to acquire advisory lock: %w", err)
	}

	// 1. Check if user already exists
	_, err = qtx.GetUserByEmail(ctx, normalizedEmail)
	if err == nil {
		// Existing account: do not issue challenge or outbox event, but commit and return generic success
		return tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("auth: failed to query user: %w", err)
	}

	// 2. Check 60-second resend cooldown against the most recent challenge
	latest, err := qtx.GetLatestRegistrationChallengeByEmail(ctx, normalizedEmail)
	if err == nil {
		if time.Since(latest.CreatedAt.Time) < s.cooldown {
			return ErrCooldownActive
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("auth: failed to query latest challenge: %w", err)
	}

	// 3. Generate token, SHA-256 hash, and AES-256-GCM ciphertext
	rawToken, err := security.GenerateToken()
	if err != nil {
		return fmt.Errorf("auth: failed to generate token: %w", err)
	}

	tokenHash := security.HashToken(rawToken)
	encryptedToken, err := s.encrypter.EncryptToken(rawToken, []byte(security.PurposeRegistrationVerification))
	if err != nil {
		return fmt.Errorf("auth: failed to encrypt token: %w", err)
	}

	// 4. Create registration verification challenge
	expiresAt := time.Now().UTC().Add(s.tokenExpiry)
	challenge, err := qtx.CreateRegistrationChallenge(ctx, sqlc.CreateRegistrationChallengeParams{
		Email:     normalizedEmail,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("auth: failed to create challenge: %w", err)
	}

	// 5. Create outbox event in the same transaction
	payloadBytes, err := json.Marshal(outbox.EmailEventPayload{
		Recipient:      normalizedEmail,
		EncryptedToken: encryptedToken,
		TokenExpiresAt: expiresAt,
	})
	if err != nil {
		return fmt.Errorf("auth: failed to marshal outbox payload: %w", err)
	}

	_, err = qtx.InsertOutboxEvent(ctx, sqlc.InsertOutboxEventParams{
		AggregateType: "registration_challenge",
		AggregateID:   challenge.ID,
		EventType:     outbox.EventTypeRegistrationVerification,
		Payload:       payloadBytes,
	})
	if err != nil {
		return fmt.Errorf("auth: failed to insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: failed to commit registration request: %w", err)
	}

	return nil
}

// CompleteRegistration verifies the submitted token, creates the user, and marks the challenge consumed.
func (s *Service) CompleteRegistration(ctx context.Context, token string, password string, displayName string) (*UserResponse, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidToken
	}

	if err := security.ValidatePasswordStrength(password); err != nil {
		return nil, ErrInvalidPassword
	}

	tokenHash := security.HashToken(token)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// 1. Lock and validate challenge using FOR UPDATE
	challenge, err := qtx.GetRegistrationChallengeForUpdate(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidToken
		}
		return nil, fmt.Errorf("auth: failed to fetch challenge: %w", err)
	}

	if challenge.ConsumedAt.Valid {
		return nil, ErrTokenAlreadyConsumed
	}

	if time.Now().UTC().After(challenge.ExpiresAt.Time.UTC()) {
		return nil, ErrTokenExpired
	}

	// 2. Serialize user creation for this email
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('user_registration:' || $1::text))", challenge.Email); err != nil {
		return nil, fmt.Errorf("auth: failed to acquire registration lock: %w", err)
	}

	// 3. Ensure no account already exists with this email
	_, err = qtx.GetUserByEmail(ctx, challenge.Email)
	if err == nil {
		// Existing account: consume challenge to invalidate it, commit, and report conflict
		_, _ = qtx.ConsumeRegistrationChallenge(ctx, challenge.ID)
		_ = tx.Commit(ctx)
		return nil, ErrEmailAlreadyRegistered
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("auth: failed to check user existence: %w", err)
	}

	// 4. Hash password with Argon2id
	passwordHash, err := security.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to hash password: %w", err)
	}

	// 5. Determine display name fallback
	dName := strings.TrimSpace(displayName)
	if dName == "" {
		parts := strings.Split(challenge.Email, "@")
		if len(parts) > 0 && parts[0] != "" {
			dName = parts[0]
		} else {
			dName = "User"
		}
	}

	// 6. Create verified user
	user, err := qtx.CreateUser(ctx, sqlc.CreateUserParams{
		Email:        challenge.Email,
		PasswordHash: passwordHash,
		DisplayName:  dName,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			_, _ = qtx.ConsumeRegistrationChallenge(ctx, challenge.ID)
			_ = tx.Commit(ctx)
			return nil, ErrEmailAlreadyRegistered
		}
		return nil, fmt.Errorf("auth: failed to create user: %w", err)
	}

	// 7. Consume challenge atomically
	rowsAffected, err := qtx.ConsumeRegistrationChallenge(ctx, challenge.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to consume challenge: %w", err)
	}
	if rowsAffected == 0 {
		return nil, ErrTokenAlreadyConsumed
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("auth: failed to commit registration completion: %w", err)
	}

	return &UserResponse{
		ID:          uuidString(user.ID),
		Email:       user.Email,
		DisplayName: user.DisplayName,
		CreatedAt:   user.CreatedAt.Time,
	}, nil
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

// uuidFromString parses a UUID string into a pgtype.UUID.
func uuidFromString(s string) (pgtype.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	var u pgtype.UUID
	copy(u.Bytes[:], id[:])
	u.Valid = true
	return u, nil
}

// ValidateSession atomically validates a session by its token hash and refreshes its
// idle expiration. Returns the updated Session on success, or ErrSessionNotFound when
// the session is unknown, revoked, idle-expired, or absolute-expired.
//
// Validation and idle-expiration refresh are performed in a single atomic UPDATE so
// concurrent requests using the same session cannot race.
// idle_expires_at is set to LEAST(now() + 30min, absolute_expires_at), ensuring the
// absolute lifetime is never extended.
func (s *Service) ValidateSession(ctx context.Context, tokenHash string) (sqlc.Session, error) {
	session, err := s.queries.ValidateAndRefreshSession(ctx, sqlc.ValidateAndRefreshSessionParams{
		TokenHash: tokenHash,
		IdleTimeout: pgtype.Interval{
			Microseconds: int64(SessionIdleTimeout / time.Microsecond),
			Valid:        true,
		},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Session{}, ErrSessionNotFound
		}
		return sqlc.Session{}, fmt.Errorf("auth: session validation failed: %w", err)
	}
	return session, nil
}

// Logout revokes the session identified by sessionID.
// Returns ErrLogoutFailed if revocation did not affect any rows (session already revoked or not found).
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	id, err := uuidFromString(sessionID)
	if err != nil {
		return fmt.Errorf("auth: invalid session ID: %w", err)
	}

	rowsAffected, err := s.queries.RevokeSessionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("auth: failed to revoke session: %w", err)
	}
	if rowsAffected == 0 {
		return ErrLogoutFailed
	}
	return nil
}

// Login authenticates a user with email and password, creates a session, and returns
// the raw session token and session info. The caller is responsible for issuing the token
// to the client only after this method returns successfully.
//
// Generic ErrInvalidCredentials is returned for all authentication failures to avoid
// leaking whether the email exists, the password is wrong, or the account is unverified.
func (s *Service) Login(ctx context.Context, email, password string) (rawToken string, info SessionInfo, err error) {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return "", SessionInfo{}, ErrInvalidCredentials
	}

	if password == "" {
		return "", SessionInfo{}, ErrInvalidCredentials
	}

	// Look up user by normalized email.
	user, err := s.queries.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// User not found: run dummy Argon2id verification to mitigate timing differences.
			_, _ = security.VerifyPassword(password, dummyPasswordHash)
			return "", SessionInfo{}, ErrInvalidCredentials
		}
		// Genuine DB error — do NOT convert to ErrInvalidCredentials.
		return "", SessionInfo{}, fmt.Errorf("auth: failed to look up user: %w", err)
	}

	// Verify password using existing Argon2id implementation.
	match, err := security.VerifyPassword(password, user.PasswordHash)
	if err != nil {
		// Hash verification error (malformed hash, incompatible algo).
		// Log this but return generic credentials error to client.
		return "", SessionInfo{}, fmt.Errorf("auth: password verification error: %w", err)
	}
	if !match {
		return "", SessionInfo{}, ErrInvalidCredentials
	}

	// Explicitly verify the account is verified (email_verified_at must be set).
	// All registered accounts have this set, but we check explicitly per security spec.
	if !user.EmailVerifiedAt.Valid {
		return "", SessionInfo{}, ErrInvalidCredentials
	}

	// Generate session token.
	rawToken, err = security.GenerateToken()
	if err != nil {
		return "", SessionInfo{}, ErrTokenGenerationFailed
	}

	tokenHash := security.HashToken(rawToken)
	now := time.Now().UTC()
	idleExpiresAt := now.Add(SessionIdleTimeout)
	absoluteExpiresAt := now.Add(SessionAbsoluteLifetime)

	// Persist session. Only issue the token if persistence succeeds.
	session, err := s.queries.CreateSession(ctx, sqlc.CreateSessionParams{
		UserID:            user.ID,
		TokenHash:         tokenHash,
		IdleExpiresAt:     pgtype.Timestamptz{Time: idleExpiresAt, Valid: true},
		AbsoluteExpiresAt: pgtype.Timestamptz{Time: absoluteExpiresAt, Valid: true},
	})
	if err != nil {
		return "", SessionInfo{}, fmt.Errorf("auth: failed to persist session: %w", err)
	}

	return rawToken, SessionInfo{
		SessionID:         uuidString(session.ID),
		UserID:            uuidString(user.ID),
		AbsoluteExpiresAt: absoluteExpiresAt,
	}, nil
}

// RequestPasswordReset initiates the password reset flow for an existing user account.
// Operates inside an atomic database transaction and guarantees:
// - Non-leaking generic responses regardless of whether the account exists.
// - 60s cooldown enforcement via PostgreSQL advisory transaction locks.
// - Tokens are only generated for existing accounts.
// - Atomically writes password reset token and transactional outbox event.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// Serialize concurrent requests for the same email using transaction-scoped advisory locks.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('password_reset:' || $1::text))", normalizedEmail); err != nil {
		return fmt.Errorf("auth: failed to acquire advisory lock: %w", err)
	}

	// 1. Check if user exists
	user, err := qtx.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nonexistent account: do not issue token or outbox event, but commit and return generic success
			return tx.Commit(ctx)
		}
		return fmt.Errorf("auth: failed to query user: %w", err)
	}

	// 2. Check 60-second cooldown against the most recent password reset token for this user
	latest, err := qtx.GetLatestPasswordResetTokenByUserID(ctx, user.ID)
	if err == nil {
		if time.Since(latest.CreatedAt.Time) < s.cooldown {
			return ErrCooldownActive
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("auth: failed to query latest password reset token: %w", err)
	}

	// 3. Generate token (256 bits entropy), SHA-256 hash, and AES-256-GCM ciphertext
	rawToken, err := security.GenerateToken()
	if err != nil {
		return fmt.Errorf("auth: failed to generate token: %w", err)
	}

	tokenHash := security.HashToken(rawToken)
	encryptedToken, err := s.encrypter.EncryptToken(rawToken, []byte(security.PurposePasswordReset))
	if err != nil {
		return fmt.Errorf("auth: failed to encrypt token: %w", err)
	}

	// 4. Create password reset token record
	expiresAt := time.Now().UTC().Add(s.resetExpiry)
	resetToken, err := qtx.CreatePasswordResetToken(ctx, sqlc.CreatePasswordResetTokenParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("auth: failed to create password reset token: %w", err)
	}

	// 5. Create outbox event in the same transaction
	payloadBytes, err := json.Marshal(outbox.EmailEventPayload{
		Recipient:      normalizedEmail,
		EncryptedToken: encryptedToken,
		TokenExpiresAt: expiresAt,
	})
	if err != nil {
		return fmt.Errorf("auth: failed to marshal outbox payload: %w", err)
	}

	_, err = qtx.InsertOutboxEvent(ctx, sqlc.InsertOutboxEventParams{
		AggregateType: "password_reset",
		AggregateID:   resetToken.ID,
		EventType:     outbox.EventTypePasswordReset,
		Payload:       payloadBytes,
	})
	if err != nil {
		return fmt.Errorf("auth: failed to insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: failed to commit password reset request: %w", err)
	}

	return nil
}

// CompletePasswordReset verifies the reset token, updates the password with Argon2id,
// marks the token consumed, and revokes all active sessions for the user within a single atomic transaction.
func (s *Service) CompletePasswordReset(ctx context.Context, token string, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidToken
	}

	if err := security.ValidatePasswordStrength(newPassword); err != nil {
		return ErrInvalidPassword
	}

	tokenHash := security.HashToken(token)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	// 1. Lock and validate password reset token using FOR UPDATE
	resetToken, err := qtx.GetPasswordResetTokenForUpdate(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		return fmt.Errorf("auth: failed to fetch password reset token: %w", err)
	}

	if resetToken.UsedAt.Valid {
		return ErrTokenAlreadyConsumed
	}

	if time.Now().UTC().After(resetToken.ExpiresAt.Time.UTC()) {
		return ErrTokenExpired
	}

	// 2. Lock user update via transaction advisory lock
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('user_password_update:' || $1::text))", uuidString(resetToken.UserID)); err != nil {
		return fmt.Errorf("auth: failed to acquire password update lock: %w", err)
	}

	// 3. Verify user still exists
	user, err := qtx.GetUserByID(ctx, resetToken.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		return fmt.Errorf("auth: failed to query user: %w", err)
	}

	// 4. Hash new password with Argon2id
	newHash, err := security.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("auth: failed to hash new password: %w", err)
	}

	// 5. Update password
	_, err = qtx.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{
		ID:           user.ID,
		PasswordHash: newHash,
	})
	if err != nil {
		return fmt.Errorf("auth: failed to update user password: %w", err)
	}

	// 6. Consume token atomically
	rowsAffected, err := qtx.ConsumePasswordResetToken(ctx, resetToken.ID)
	if err != nil {
		return fmt.Errorf("auth: failed to consume reset token: %w", err)
	}
	if rowsAffected == 0 {
		return ErrTokenAlreadyConsumed
	}

	// 7. Revoke ALL existing active sessions for this user
	_, err = qtx.RevokeAllSessionsByUserID(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("auth: failed to revoke user sessions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: failed to commit password reset completion: %w", err)
	}

	return nil
}

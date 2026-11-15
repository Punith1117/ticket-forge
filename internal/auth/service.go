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
)

// Service provides user registration business operations.
type Service struct {
	pool        *pgxpool.Pool
	queries     *sqlc.Queries
	encrypter   *security.TokenEncrypter
	cooldown    time.Duration
	tokenExpiry time.Duration
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

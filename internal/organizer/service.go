package organizer

import (
	"context"
	"errors"
	"fmt"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound    = errors.New("organizer: user not found")
	ErrProfileNotFound = errors.New("organizer: profile not found")
)

type Service struct {
	queries *sqlc.Queries
}

func NewService(pool *pgxpool.Pool) (*Service, error) {
	if pool == nil {
		return nil, errors.New("organizer: database pool cannot be nil")
	}

	return &Service{
		queries: sqlc.New(pool),
	}, nil
}

// CreateOrGetProfile creates an organizer profile for the user.
// If one already exists, it returns the existing profile without modifying it.
func (s *Service) CreateOrGetProfile(
	ctx context.Context,
	userID string,
) (sqlc.OrganizerProfile, bool, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return sqlc.OrganizerProfile{}, false, fmt.Errorf(
			"organizer: invalid user ID: %w", err,
		)
	}

	var dbID pgtype.UUID
	copy(dbID.Bytes[:], id[:])
	dbID.Valid = true

	profile, err := s.queries.CreateOrganizerProfile(ctx, dbID)
	if err == nil {
		return profile, true, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.OrganizerProfile{}, false, fmt.Errorf(
			"organizer: failed to create profile: %w", err,
		)
	}

	// No row was inserted. It may already exist, so retrieve it.
	profile, err = s.queries.GetOrganizerProfileByUserID(ctx, dbID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.OrganizerProfile{}, false, ErrUserNotFound
		}

		return sqlc.OrganizerProfile{}, false, fmt.Errorf(
			"organizer: failed to retrieve existing profile: %w", err,
		)
	}

	return profile, false, nil
}

// GetProfileByUserID retrieves an existing organizer profile for a user.
func (s *Service) GetProfileByUserID(
	ctx context.Context,
	userID string,
) (sqlc.OrganizerProfile, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return sqlc.OrganizerProfile{}, fmt.Errorf(
			"organizer: invalid user ID: %w", err,
		)
	}

	var dbID pgtype.UUID
	copy(dbID.Bytes[:], id[:])
	dbID.Valid = true

	profile, err := s.queries.GetOrganizerProfileByUserID(ctx, dbID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.OrganizerProfile{}, ErrProfileNotFound
		}

		return sqlc.OrganizerProfile{}, fmt.Errorf(
			"organizer: failed to retrieve profile: %w", err,
		)
	}

	return profile, nil
}

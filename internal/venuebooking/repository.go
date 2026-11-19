package venuebooking

import (
	"context"
	"errors"
	"fmt"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) (*Repository, error) {
	if pool == nil {
		return nil, errors.New("venuebooking: database pool cannot be nil")
	}

	return &Repository{
		pool: pool,
	}, nil
}

// LockOrganizerProfile serializes venue booking creation
// for the same organizer within a transaction.
func (r *Repository) LockOrganizerProfile(
	ctx context.Context,
	tx pgx.Tx,
	organizerProfileID pgtype.UUID,
) error {
	_, err := sqlc.New(tx).LockOrganizerProfile(ctx, organizerProfileID)
	if err != nil {
		return fmt.Errorf("venuebooking: failed to lock organizer profile: %w", err)
	}

	return nil
}

// GetActiveVenueForBooking retrieves an active venue while
// holding a share lock against concurrent venue deactivation.
func (r *Repository) GetActiveVenueForBooking(
	ctx context.Context,
	tx pgx.Tx,
	venueID pgtype.UUID,
) (sqlc.GetActiveVenueForBookingRow, error) {
	venue, err := sqlc.New(tx).GetActiveVenueForBooking(ctx, venueID)
	if err != nil {
		return sqlc.GetActiveVenueForBookingRow{}, fmt.Errorf(
			"venuebooking: failed to retrieve active venue: %w", err,
		)
	}

	return venue, nil
}

// CountActiveUnfinishedVenueBookings returns the organizer's
// active venue bookings that have not yet ended.
func (r *Repository) CountActiveUnfinishedVenueBookings(
	ctx context.Context,
	tx pgx.Tx,
	organizerProfileID pgtype.UUID,
) (int32, error) {
	count, err := sqlc.New(tx).CountActiveUnfinishedVenueBookings(
		ctx,
		organizerProfileID,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"venuebooking: failed to count active bookings: %w", err,
		)
	}

	return count, nil
}

// CreateVenueBooking inserts a venue booking.
func (r *Repository) CreateVenueBooking(
	ctx context.Context,
	tx pgx.Tx,
	params sqlc.CreateVenueBookingParams,
) (sqlc.CreateVenueBookingRow, error) {
	booking, err := sqlc.New(tx).CreateVenueBooking(ctx, params)
	if err != nil {
		return sqlc.CreateVenueBookingRow{}, fmt.Errorf(
			"venuebooking: failed to create booking: %w", err,
		)
	}

	return booking, nil
}

// GetVenueBookingByID retrieves a venue booking by ID.
func (r *Repository) GetVenueBookingByID(
	ctx context.Context,
	id pgtype.UUID,
) (sqlc.GetVenueBookingByIDRow, error) {
	booking, err := sqlc.New(r.pool).GetVenueBookingByID(ctx, id)
	if err != nil {
		return sqlc.GetVenueBookingByIDRow{}, fmt.Errorf(
			"venuebooking: failed to retrieve booking: %w", err,
		)
	}

	return booking, nil
}

// GetVenueBookingForOrganizer retrieves a booking belonging
// to a specific organizer.
func (r *Repository) GetVenueBookingForOrganizer(
	ctx context.Context,
	organizerProfileID pgtype.UUID,
	bookingID pgtype.UUID,
) (sqlc.GetVenueBookingForOrganizerRow, error) {
	booking, err := sqlc.New(r.pool).GetVenueBookingForOrganizer(
		ctx,
		sqlc.GetVenueBookingForOrganizerParams{
			ID:                 bookingID,
			OrganizerProfileID: organizerProfileID,
		},
	)
	if err != nil {
		return sqlc.GetVenueBookingForOrganizerRow{}, fmt.Errorf(
			"venuebooking: failed to retrieve organizer booking: %w", err,
		)
	}

	return booking, nil
}

// ListOrganizerVenueBookings retrieves all venue bookings
// belonging to an organizer.
func (r *Repository) ListOrganizerVenueBookings(
	ctx context.Context,
	organizerProfileID pgtype.UUID,
) ([]sqlc.ListOrganizerVenueBookingsRow, error) {
	bookings, err := sqlc.New(r.pool).ListOrganizerVenueBookings(
		ctx,
		organizerProfileID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"venuebooking: failed to list organizer bookings: %w", err,
		)
	}

	return bookings, nil
}

// LockVenueBookingForOrganizer locks a booking belonging
// to a specific organizer within a transaction.
func (r *Repository) LockVenueBookingForOrganizer(
	ctx context.Context,
	tx pgx.Tx,
	organizerProfileID pgtype.UUID,
	bookingID pgtype.UUID,
) (sqlc.LockVenueBookingForOrganizerRow, error) {
	booking, err := sqlc.New(tx).LockVenueBookingForOrganizer(
		ctx,
		sqlc.LockVenueBookingForOrganizerParams{
			ID:                 bookingID,
			OrganizerProfileID: organizerProfileID,
		},
	)
	if err != nil {
		return sqlc.LockVenueBookingForOrganizerRow{}, fmt.Errorf(
			"venuebooking: failed to lock booking: %w", err,
		)
	}

	return booking, nil
}

// HasActiveEventForVenueBooking checks whether a booking
// has an associated active event.
func (r *Repository) HasActiveEventForVenueBooking(
	ctx context.Context,
	tx pgx.Tx,
	bookingID pgtype.UUID,
) (bool, error) {
	exists, err := sqlc.New(tx).HasActiveEventForVenueBooking(
		ctx,
		bookingID,
	)
	if err != nil {
		return false, fmt.Errorf(
			"venuebooking: failed to check active event: %w", err,
		)
	}

	return exists, nil
}

// CancelVenueBooking cancels an active booking belonging
// to a specific organizer.
func (r *Repository) CancelVenueBooking(
	ctx context.Context,
	tx pgx.Tx,
	organizerProfileID pgtype.UUID,
	bookingID pgtype.UUID,
) (sqlc.CancelVenueBookingRow, error) {
	booking, err := sqlc.New(tx).CancelVenueBooking(
		ctx,
		sqlc.CancelVenueBookingParams{
			ID:                 bookingID,
			OrganizerProfileID: organizerProfileID,
		},
	)
	if err != nil {
		return sqlc.CancelVenueBookingRow{}, fmt.Errorf(
			"venuebooking: failed to cancel booking: %w", err,
		)
	}

	return booking, nil
}

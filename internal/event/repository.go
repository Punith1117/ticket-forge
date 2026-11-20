package event

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
		return nil, errors.New("event: database pool cannot be nil")
	}

	return &Repository{
		pool: pool,
	}, nil
}

func (r *Repository) LockVenueBookingForOrganizer(
	ctx context.Context,
	tx pgx.Tx,
	venueBookingID pgtype.UUID,
	organizerProfileID pgtype.UUID,
) (sqlc.LockVenueBookingForOrganizerRow, error) {
	booking, err := sqlc.New(tx).LockVenueBookingForOrganizer(
		ctx,
		sqlc.LockVenueBookingForOrganizerParams{
			ID:                 venueBookingID,
			OrganizerProfileID: organizerProfileID,
		},
	)
	if err != nil {
		return sqlc.LockVenueBookingForOrganizerRow{}, fmt.Errorf(
			"event: lock venue booking: %w",
			err,
		)
	}

	return booking, nil
}

func (r *Repository) GetVenueCapacityForBooking(
	ctx context.Context,
	tx pgx.Tx,
	venueBookingID pgtype.UUID,
) (int32, error) {
	capacity, err := sqlc.New(tx).GetVenueCapacityForBooking(
		ctx,
		venueBookingID,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"event: get venue capacity: %w",
			err,
		)
	}

	return capacity, nil
}

func (r *Repository) HasActiveEventForVenueBooking(
	ctx context.Context,
	tx pgx.Tx,
	venueBookingID pgtype.UUID,
) (bool, error) {
	exists, err := sqlc.New(tx).HasActiveEventForVenueBooking(
		ctx,
		venueBookingID,
	)
	if err != nil {
		return false, fmt.Errorf(
			"event: check active event: %w",
			err,
		)
	}

	return exists, nil
}

func (r *Repository) CreateEvent(
	ctx context.Context,
	tx pgx.Tx,
	params sqlc.CreateEventParams,
) (sqlc.Event, error) {
	createdEvent, err := sqlc.New(tx).CreateEvent(ctx, params)
	if err != nil {
		return sqlc.Event{}, fmt.Errorf(
			"event: create event: %w",
			err,
		)
	}

	return createdEvent, nil
}

func (r *Repository) CreateTicketCategory(
	ctx context.Context,
	tx pgx.Tx,
	params sqlc.CreateTicketCategoryParams,
) (sqlc.TicketCategory, error) {
	category, err := sqlc.New(tx).CreateTicketCategory(ctx, params)
	if err != nil {
		return sqlc.TicketCategory{}, fmt.Errorf(
			"event: create ticket category: %w",
			err,
		)
	}

	return category, nil
}

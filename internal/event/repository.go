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

// LockActiveEventForOrganizer locks an active event belonging
// to the specified organizer within a transaction.
func (r *Repository) LockActiveEventForOrganizer(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
	organizerProfileID pgtype.UUID,
) (sqlc.Event, error) {
	var event sqlc.Event

	err := tx.QueryRow(ctx, `
		SELECT
			id,
			organizer_profile_id,
			title,
			description,
			status,
			booking_status,
			created_at,
			updated_at,
			venue_booking_id
		FROM events
		WHERE id = $1
		  AND organizer_profile_id = $2
		FOR UPDATE
	`,
		eventID,
		organizerProfileID,
	).Scan(
		&event.ID,
		&event.OrganizerProfileID,
		&event.Title,
		&event.Description,
		&event.Status,
		&event.BookingStatus,
		&event.CreatedAt,
		&event.UpdatedAt,
		&event.VenueBookingID,
	)
	if err != nil {
		return sqlc.Event{}, fmt.Errorf(
			"event: lock event for organizer: %w",
			err,
		)
	}

	return event, nil
}

// UpdateActiveEventBookingStatus updates the booking status
// of an active event within a transaction.
func (r *Repository) UpdateActiveEventBookingStatus(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
	bookingStatus string,
) (sqlc.Event, error) {
	updatedEvent, err := sqlc.New(tx).UpdateActiveEventBookingStatus(
		ctx,
		sqlc.UpdateActiveEventBookingStatusParams{
			ID:            eventID,
			BookingStatus: bookingStatus,
		},
	)
	if err != nil {
		return sqlc.Event{}, fmt.Errorf(
			"event: update booking status: %w",
			err,
		)
	}

	return updatedEvent, nil
}

func (r *Repository) CreateInventory(
	ctx context.Context,
	tx pgx.Tx,
	ticketCategoryID pgtype.UUID,
) error {
	err := sqlc.New(tx).CreateInventory(ctx, ticketCategoryID)
	if err != nil {
		return fmt.Errorf("event: create inventory: %w", err)
	}

	return nil
}

// CancelEvent updates an active event's status to 'cancelled' and booking_status to 'paused'.
func (r *Repository) CancelEvent(
	ctx context.Context,
	tx pgx.Tx,
	params sqlc.CancelEventParams,
) (sqlc.Event, error) {
	cancelledEvent, err := sqlc.New(tx).CancelEvent(ctx, params)
	if err != nil {
		return sqlc.Event{}, fmt.Errorf("event: cancel event: %w", err)
	}

	return cancelledEvent, nil
}

// InsertOutboxEvent inserts a transactional outbox event.
func (r *Repository) InsertOutboxEvent(
	ctx context.Context,
	tx pgx.Tx,
	params sqlc.InsertOutboxEventParams,
) (sqlc.OutboxEvent, error) {
	outboxEvent, err := sqlc.New(tx).InsertOutboxEvent(ctx, params)
	if err != nil {
		return sqlc.OutboxEvent{}, fmt.Errorf("event: insert outbox event: %w", err)
	}

	return outboxEvent, nil
}

// GetEventCancellationSummary retrieves cancellation and refund progress metrics.
func (r *Repository) GetEventCancellationSummary(
	ctx context.Context,
	eventID pgtype.UUID,
	organizerProfileID pgtype.UUID,
) (sqlc.GetEventCancellationSummaryRow, error) {
	summary, err := sqlc.New(r.pool).GetEventCancellationSummary(ctx, sqlc.GetEventCancellationSummaryParams{
		ID:                 eventID,
		OrganizerProfileID: organizerProfileID,
	})
	if err != nil {
		return sqlc.GetEventCancellationSummaryRow{}, fmt.Errorf("event: get cancellation summary: %w", err)
	}

	return summary, nil
}

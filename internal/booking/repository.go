package booking

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
		return nil, errors.New("booking: database pool cannot be nil")
	}

	return &Repository{
		pool: pool,
	}, nil
}

func (r *Repository) LockBookableEvent(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
) (sqlc.LockBookableEventRow, error) {
	event, err := sqlc.New(tx).LockBookableEvent(ctx, eventID)
	if err != nil {
		return sqlc.LockBookableEventRow{}, fmt.Errorf(
			"booking: lock bookable event: %w",
			err,
		)
	}

	return event, nil
}

func (r *Repository) LockActiveTicketCategory(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
	categoryID pgtype.UUID,
) (sqlc.LockActiveTicketCategoryRow, error) {
	category, err := sqlc.New(tx).LockActiveTicketCategory(
		ctx,
		sqlc.LockActiveTicketCategoryParams{
			EventID:          eventID,
			TicketCategoryID: categoryID,
		},
	)
	if err != nil {
		return sqlc.LockActiveTicketCategoryRow{}, fmt.Errorf(
			"booking: lock active ticket category: %w",
			err,
		)
	}

	return category, nil
}

func (r *Repository) LockInventory(
	ctx context.Context,
	tx pgx.Tx,
	categoryID pgtype.UUID,
) (sqlc.LockInventoryRow, error) {
	inventory, err := sqlc.New(tx).LockInventory(ctx, categoryID)
	if err != nil {
		return sqlc.LockInventoryRow{}, fmt.Errorf(
			"booking: lock inventory: %w",
			err,
		)
	}

	return inventory, nil
}

func (r *Repository) IncrementReservedInventory(
	ctx context.Context,
	tx pgx.Tx,
	categoryID pgtype.UUID,
	quantity int32,
) (int64, error) {
	rowsAffected, err := sqlc.New(tx).IncrementReservedInventory(
		ctx,
		sqlc.IncrementReservedInventoryParams{
			TicketCategoryID: categoryID,
			Quantity:         quantity,
		},
	)
	if err != nil {
		return 0, fmt.Errorf(
			"booking: increment reserved inventory: %w",
			err,
		)
	}

	return rowsAffected, nil
}

func (r *Repository) CreateReservation(
	ctx context.Context,
	tx pgx.Tx,
	customerID pgtype.UUID,
	eventID pgtype.UUID,
	categoryID pgtype.UUID,
	quantity int32,
	holdDuration pgtype.Interval,
) (sqlc.Reservation, error) {
	reservation, err := sqlc.New(tx).CreateReservation(
		ctx,
		sqlc.CreateReservationParams{
			CustomerID:       customerID,
			EventID:          eventID,
			TicketCategoryID: categoryID,
			Quantity:         quantity,
			HoldDuration:     holdDuration,
		},
	)
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf(
			"booking: create reservation: %w",
			err,
		)
	}

	return reservation, nil
}

func (r *Repository) ExpireExpiredReservationsForCategory(
	ctx context.Context,
	tx pgx.Tx,
	categoryID pgtype.UUID,
) (int32, error) {
	releasedQuantity, err := sqlc.New(tx).ExpireExpiredReservationsForCategory(
		ctx,
		categoryID,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"booking: expire expired reservations for category: %w",
			err,
		)
	}

	return releasedQuantity, nil
}

func (r *Repository) DecrementReservedInventory(
	ctx context.Context,
	tx pgx.Tx,
	categoryID pgtype.UUID,
	quantity int32,
) (int64, error) {
	rowsAffected, err := sqlc.New(tx).DecrementReservedInventory(
		ctx,
		sqlc.DecrementReservedInventoryParams{
			TicketCategoryID: categoryID,
			Quantity:         quantity,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("booking: decrement reserved inventory: %w", err)
	}

	return rowsAffected, nil
}

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

func (r *Repository) GetReservationLifecycleReference(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (sqlc.GetReservationLifecycleReferenceRow, error) {
	ref, err := sqlc.New(tx).GetReservationLifecycleReference(ctx, reservationID)
	if err != nil {
		return sqlc.GetReservationLifecycleReferenceRow{}, fmt.Errorf("booking: get reservation lifecycle ref: %w", err)
	}
	return ref, nil
}

func (r *Repository) LockEventForReservationLifecycle(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
) (sqlc.LockEventForReservationLifecycleRow, error) {
	event, err := sqlc.New(tx).LockEventForReservationLifecycle(ctx, eventID)
	if err != nil {
		return sqlc.LockEventForReservationLifecycleRow{}, fmt.Errorf("booking: lock event for lifecycle: %w", err)
	}
	return event, nil
}

func (r *Repository) GetReservationForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (sqlc.Reservation, error) {
	res, err := sqlc.New(tx).GetReservationForUpdate(ctx, reservationID)
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf("booking: get reservation for update: %w", err)
	}
	return res, nil
}

func (r *Repository) ConfirmReservation(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (int64, error) {
	rows, err := sqlc.New(tx).ConfirmReservation(ctx, reservationID)
	if err != nil {
		return 0, fmt.Errorf("booking: confirm reservation: %w", err)
	}
	return rows, nil
}

func (r *Repository) ConvertReservedToSoldInventory(
	ctx context.Context,
	tx pgx.Tx,
	categoryID pgtype.UUID,
	quantity int32,
) (int64, error) {
	rows, err := sqlc.New(tx).ConvertReservedToSoldInventory(ctx, sqlc.ConvertReservedToSoldInventoryParams{
		TicketCategoryID: categoryID,
		Quantity:         quantity,
	})
	if err != nil {
		return 0, fmt.Errorf("booking: convert reserved to sold inventory: %w", err)
	}
	return rows, nil
}

func (r *Repository) CreateBooking(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.CreateBookingParams,
) (sqlc.Booking, error) {
	booking, err := sqlc.New(tx).CreateBooking(ctx, arg)
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("booking: create booking: %w", err)
	}
	return booking, nil
}

func (r *Repository) GetBookingByReservationID(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (sqlc.Booking, error) {
	booking, err := sqlc.New(tx).GetBookingByReservationID(ctx, reservationID)
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("booking: get booking by reservation id: %w", err)
	}
	return booking, nil
}

func (r *Repository) GetPaymentAttemptByID(
	ctx context.Context,
	tx pgx.Tx,
	attemptID pgtype.UUID,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).GetPaymentAttemptByID(ctx, attemptID)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("booking: get payment attempt by id: %w", err)
	}
	return attempt, nil
}

func (r *Repository) GetReservationStatusForCustomer(
	ctx context.Context,
	reservationID pgtype.UUID,
	customerID pgtype.UUID,
) (sqlc.GetReservationStatusForCustomerRow, error) {
	row, err := sqlc.New(r.pool).GetReservationStatusForCustomer(
		ctx,
		sqlc.GetReservationStatusForCustomerParams{
			ID:         reservationID,
			CustomerID: customerID,
		},
	)
	if err != nil {
		return sqlc.GetReservationStatusForCustomerRow{}, fmt.Errorf(
			"booking: get reservation status: %w",
			err,
		)
	}

	return row, nil
}

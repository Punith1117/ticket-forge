package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Punith1117/ticket-forge/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	createReservationOperation = "reservation.create"
	reservationHoldDuration    = 2 * time.Minute
)

type Service struct {
	pool        *pgxpool.Pool
	repository  *Repository
	idempotency *idempotency.Service
}

func NewService(pool *pgxpool.Pool) (*Service, error) {
	if pool == nil {
		return nil, errors.New("booking: database pool cannot be nil")
	}

	repository, err := NewRepository(pool)
	if err != nil {
		return nil, err
	}

	return &Service{
		pool:        pool,
		repository:  repository,
		idempotency: idempotency.NewService(),
	}, nil
}

func (s *Service) CreateReservation(
	ctx context.Context,
	customerID string,
	eventID string,
	req CreateReservationRequest,
	idempotencyKey string,
) (CreateReservationResponse, error) {
	var response CreateReservationResponse

	if req.Quantity <= 0 {
		return response, ErrInvalidQuantity
	}

	if strings.TrimSpace(req.TicketCategoryID) == "" {
		return CreateReservationResponse{}, ErrMissingTicketCategoryID
	}

	customerUUID, err := uuid.Parse(customerID)
	if err != nil {
		return response, fmt.Errorf("booking: parse customer ID: %w", err)
	}

	eventUUID, err := uuid.Parse(eventID)
	if err != nil {
		return response, ErrInvalidEventID
	}

	categoryUUID, err := uuid.Parse(req.TicketCategoryID)
	if err != nil {
		return response, ErrInvalidTicketCategoryID
	}

	customerDBID := pgtype.UUID{Bytes: customerUUID, Valid: true}
	eventDBID := pgtype.UUID{Bytes: eventUUID, Valid: true}
	categoryDBID := pgtype.UUID{Bytes: categoryUUID, Valid: true}

	// Hash a normalized representation of the complete operation.
	// The event ID is part of the payload even though it comes from the URL.
	payload, err := json.Marshal(struct {
		EventID          string `json:"event_id"`
		TicketCategoryID string `json:"ticket_category_id"`
		Quantity         int32  `json:"quantity"`
	}{
		EventID:          eventUUID.String(),
		TicketCategoryID: categoryUUID.String(),
		Quantity:         req.Quantity,
	})
	if err != nil {
		return response, fmt.Errorf("booking: marshal idempotency payload: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return response, fmt.Errorf("booking: begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	execution, err := s.idempotency.Begin(ctx, tx, idempotency.Request{
		UserID:    customerUUID.String(),
		Operation: createReservationOperation,
		Key:       idempotencyKey,
		Payload:   payload,
	})
	if err != nil {
		return response, fmt.Errorf("booking: begin idempotency execution: %w", err)
	}

	// A replay returns the exact response from the original operation.
	if execution.Replay != nil {
		if err := json.Unmarshal(execution.Replay.Body, &response); err != nil {
			return response, fmt.Errorf("booking: decode idempotent response: %w", err)
		}

		if err := tx.Rollback(ctx); err != nil {
			return response, fmt.Errorf("booking: rollback replay transaction: %w", err)
		}

		return response, nil
	}

	// Lock order matters. Keep this order consistent with other
	// reservation lifecycle operations as we implement them.
	_, err = s.repository.LockBookableEvent(ctx, tx, eventDBID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrEventNotBookable
		}
		return response, fmt.Errorf("booking: validate event: %w", err)
	}

	category, err := s.repository.LockActiveTicketCategory(
		ctx,
		tx,
		eventDBID,
		categoryDBID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrTicketCategoryUnavailable
		}
		return response, fmt.Errorf("booking: validate ticket category: %w", err)
	}

	// First, expire stale reservations belonging to this category.
	// The event and category are already locked.
	releasedQuantity, err := s.repository.ExpireExpiredReservationsForCategory(
		ctx,
		tx,
		categoryDBID,
	)
	if err != nil {
		return response, fmt.Errorf(
			"booking: expire stale reservations: %w",
			err,
		)
	}

	// Release the inventory occupied by those expired reservations.
	if releasedQuantity > 0 {
		rowsAffected, err := s.repository.DecrementReservedInventory(
			ctx,
			tx,
			categoryDBID,
			releasedQuantity,
		)
		if err != nil {
			return response, fmt.Errorf(
				"booking: release expired reservation inventory: %w",
				err,
			)
		}

		if rowsAffected != 1 {
			return response, fmt.Errorf(
				"booking: inventory inconsistency while releasing %d expired tickets",
				releasedQuantity,
			)
		}
	}

	// Now lock inventory and check availability.
	inventory, err := s.repository.LockInventory(ctx, tx, categoryDBID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return response, ErrInventoryUnavailable
		}
		return response, fmt.Errorf("booking: lock inventory: %w", err)
	}

	available := category.Capacity -
		inventory.ReservedQuantity -
		inventory.SoldQuantity

	if available < req.Quantity {
		return response, ErrInsufficientInventory
	}

	rowsAffected, err := s.repository.IncrementReservedInventory(
		ctx,
		tx,
		categoryDBID,
		req.Quantity,
	)
	if err != nil {
		return response, fmt.Errorf("booking: reserve inventory: %w", err)
	}
	if rowsAffected != 1 {
		return response, ErrInsufficientInventory
	}

	holdDuration := pgtype.Interval{
		Microseconds: int64(reservationHoldDuration / time.Microsecond),
		Valid:        true,
	}

	reservation, err := s.repository.CreateReservation(
		ctx,
		tx,
		customerDBID,
		eventDBID,
		categoryDBID,
		req.Quantity,
		holdDuration,
	)
	if err != nil {
		return response, fmt.Errorf("booking: create reservation: %w", err)
	}

	response = CreateReservationResponse{
		ID:               uuid.UUID(reservation.ID.Bytes).String(),
		EventID:          eventUUID.String(),
		TicketCategoryID: categoryUUID.String(),
		Quantity:         reservation.Quantity,
		UnitPriceMinor:   reservation.UnitPriceMinor,
		TotalPriceMinor:  reservation.TotalPriceMinor,
		Currency:         reservation.Currency,
		Status:           reservation.Status,
		ExpiresAt:        reservation.ExpiresAt.Time,
	}

	responseBody, err := json.Marshal(response)
	if err != nil {
		return CreateReservationResponse{}, fmt.Errorf("booking: marshal response: %w", err)
	}

	if err := s.idempotency.Complete(ctx, tx, execution, 201, responseBody); err != nil {
		return CreateReservationResponse{}, fmt.Errorf("booking: complete idempotency execution: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateReservationResponse{}, fmt.Errorf("booking: commit transaction: %w", err)
	}

	return response, nil
}

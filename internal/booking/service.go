package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
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

// ConfirmReservationPayment confirms a held reservation, updates inventory, and creates a booking within an existing transaction.
func (s *Service) ConfirmReservationPayment(
	ctx context.Context,
	tx pgx.Tx,
	reservationIDStr string,
	paymentAttemptIDStr string,
) (ConfirmationResult, error) {
	resUUID, err := uuid.Parse(reservationIDStr)
	if err != nil {
		return ConfirmationResult{}, ErrReservationNotFound
	}
	attemptUUID, err := uuid.Parse(paymentAttemptIDStr)
	if err != nil {
		return ConfirmationResult{}, ErrPaymentAttemptMismatch
	}

	resDBID := pgtype.UUID{Bytes: resUUID, Valid: true}
	attemptDBID := pgtype.UUID{Bytes: attemptUUID, Valid: true}

	// 1. Identify reservation and its event
	ref, err := s.repository.GetReservationLifecycleReference(ctx, tx, resDBID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfirmationResult{
				Status:           ConfirmationStatusIneligible,
				IneligibleReason: ErrReservationNotFound,
			}, nil
		}
		return ConfirmationResult{}, fmt.Errorf("booking: get reservation lifecycle ref: %w", err)
	}

	// 2. Lock the relevant event row (Lock order: 1. Event)
	eventRow, err := s.repository.LockEventForReservationLifecycle(ctx, tx, ref.EventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfirmationResult{
				Status:           ConfirmationStatusIneligible,
				IneligibleReason: ErrEventNotEligible,
			}, nil
		}
		return ConfirmationResult{}, fmt.Errorf("booking: lock event for lifecycle: %w", err)
	}

	// 3. Verify event is still eligible for confirmation (cancelled events reject confirmation)
	if eventRow.Status != "active" {
		return ConfirmationResult{
			Status:           ConfirmationStatusIneligible,
			IneligibleReason: ErrEventCancelled,
		}, nil
	}

	// 4. Lock the reservation row (Lock order: 2. Reservation)
	res, err := s.repository.GetReservationForUpdate(ctx, tx, resDBID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfirmationResult{
				Status:           ConfirmationStatusIneligible,
				IneligibleReason: ErrReservationNotFound,
			}, nil
		}
		return ConfirmationResult{}, fmt.Errorf("booking: lock reservation for update: %w", err)
	}

	// 5. Verify payment attempt belongs to the reservation and has succeeded
	attempt, err := s.repository.GetPaymentAttemptByID(ctx, tx, attemptDBID)
	if err != nil {
		return ConfirmationResult{}, fmt.Errorf("booking: verify payment attempt: %w", err)
	}
	if attempt.ReservationID != resDBID {
		return ConfirmationResult{}, ErrPaymentAttemptMismatch
	}
	if attempt.Status != "succeeded" {
		return ConfirmationResult{}, fmt.Errorf("booking: payment attempt status is %s, expected succeeded", attempt.Status)
	}

	// 6. Verify reservation is still held
	if res.Status != "held" {
		return ConfirmationResult{
			Status:           ConfirmationStatusIneligible,
			IneligibleReason: ErrReservationNotHeld,
		}, nil
	}

	// 7. Verify reservation has not expired
	if res.ExpiresAt.Valid && !time.Now().UTC().Before(res.ExpiresAt.Time) {
		return ConfirmationResult{
			Status:           ConfirmationStatusIneligible,
			IneligibleReason: ErrReservationExpired,
		}, nil
	}

	// 8. Lock the relevant inventory row (Lock order: 3. Inventory)
	inv, err := s.repository.LockInventory(ctx, tx, res.TicketCategoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfirmationResult{
				Status:           ConfirmationStatusIneligible,
				IneligibleReason: ErrInventoryUnavailable,
			}, nil
		}
		return ConfirmationResult{}, fmt.Errorf("booking: lock inventory: %w", err)
	}

	// 9. Verify inventory counters are consistent with reservation
	if inv.ReservedQuantity < res.Quantity {
		return ConfirmationResult{
			Status:           ConfirmationStatusIneligible,
			IneligibleReason: ErrInventoryInconsistent,
		}, nil
	}

	// 10. Transition reservation from held to confirmed (conditional on held & unexpired in DB clock)
	confirmRows, err := s.repository.ConfirmReservation(ctx, tx, resDBID)
	if err != nil {
		return ConfirmationResult{}, fmt.Errorf("booking: confirm reservation: %w", err)
	}
	if confirmRows != 1 {
		// Clock expired or status changed
		return ConfirmationResult{
			Status:           ConfirmationStatusIneligible,
			IneligibleReason: ErrReservationExpired,
		}, nil
	}

	// 11. Convert inventory (decrement reserved, increment sold)
	convertRows, err := s.repository.ConvertReservedToSoldInventory(ctx, tx, res.TicketCategoryID, res.Quantity)
	if err != nil {
		return ConfirmationResult{}, fmt.Errorf("booking: convert inventory: %w", err)
	}
	if convertRows != 1 {
		return ConfirmationResult{}, fmt.Errorf("booking: convert inventory affected %d rows, expected 1", convertRows)
	}

	// 12. Create the corresponding booking
	bookingID := uuid.New()
	createdBooking, err := s.repository.CreateBooking(ctx, tx, sqlc.CreateBookingParams{
		ID:               pgtype.UUID{Bytes: bookingID, Valid: true},
		ReservationID:    res.ID,
		PaymentAttemptID: attemptDBID,
		CustomerID:       res.CustomerID,
		Quantity:         res.Quantity,
		UnitPriceMinor:   res.UnitPriceMinor,
		TotalPriceMinor:  res.TotalPriceMinor,
		Currency:         res.Currency,
	})
	if err != nil {
		return ConfirmationResult{}, fmt.Errorf("booking: create booking: %w", err)
	}

	return ConfirmationResult{
		Status:        ConfirmationStatusConfirmed,
		BookingID:     uuid.UUID(createdBooking.ID.Bytes).String(),
		ReservationID: resUUID.String(),
	}, nil
}

// GetReservationStatus retrieves the current reservation, payment, booking, and refund state for polling.
func (s *Service) GetReservationStatus(
	ctx context.Context,
	customerID string,
	reservationID string,
) (ReservationStatusResponse, error) {
	customerUUID, err := uuid.Parse(customerID)
	if err != nil {
		return ReservationStatusResponse{}, fmt.Errorf("booking: parse customer ID: %w", err)
	}

	resUUID, err := uuid.Parse(reservationID)
	if err != nil {
		return ReservationStatusResponse{}, ErrInvalidReservationID
	}

	row, err := s.repository.GetReservationStatusForCustomer(
		ctx,
		pgtype.UUID{Bytes: resUUID, Valid: true},
		pgtype.UUID{Bytes: customerUUID, Valid: true},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReservationStatusResponse{}, ErrReservationNotFound
		}
		return ReservationStatusResponse{}, err
	}

	paymentStatus := ""
	if row.PaymentAttemptID.Valid {
		paymentStatus = row.PaymentStatus
	}

	refundStatus := ""
	if row.RefundID.Valid {
		refundStatus = row.RefundStatus
	}

	if row.EffectiveReservationStatus == "confirmed" && !row.BookingID.Valid {
		return ReservationStatusResponse{}, fmt.Errorf(
			"%w: confirmed reservation missing booking record",
			ErrInconsistentReservationState,
		)
	}
	if row.BookingID.Valid && row.EffectiveReservationStatus != "confirmed" {
		return ReservationStatusResponse{}, fmt.Errorf(
			"%w: booking record exists for reservation_status=%s",
			ErrInconsistentReservationState,
			row.EffectiveReservationStatus,
		)
	}

	outcome, isTerminal, err := deriveReservationOutcome(
		row.EffectiveReservationStatus,
		paymentStatus,
		refundStatus,
	)
	if err != nil {
		return ReservationStatusResponse{}, err
	}

	resp := ReservationStatusResponse{
		Outcome:    outcome,
		IsTerminal: isTerminal,
		Reservation: ReservationSummary{
			ID:                 uuid.UUID(row.ID.Bytes).String(),
			EventID:            uuid.UUID(row.EventID.Bytes).String(),
			TicketCategoryID:   uuid.UUID(row.TicketCategoryID.Bytes).String(),
			Quantity:           row.Quantity,
			UnitPriceMinor:     row.UnitPriceMinor,
			TotalPriceMinor:    row.TotalPriceMinor,
			Currency:           row.Currency,
			Status:             row.EffectiveReservationStatus,
			CancellationReason: textPtr(row.CancellationReason),
			ExpiresAt:          row.ExpiresAt.Time,
			CreatedAt:          row.CreatedAt.Time,
			UpdatedAt:          row.UpdatedAt.Time,
		},
	}

	if row.PaymentAttemptID.Valid {
		resp.Payment = &PaymentSummary{
			AttemptID:     uuid.UUID(row.PaymentAttemptID.Bytes).String(),
			Status:        row.PaymentStatus,
			Provider:      row.PaymentProvider,
			AmountMinor:   row.PaymentAmountMinor,
			Currency:      row.PaymentCurrency,
			FailureReason: textPtr(row.PaymentFailureReason),
			AttemptedAt:   row.PaymentAttemptedAt.Time,
			CompletedAt:   timePtr(row.PaymentCompletedAt),
		}
	}

	if row.BookingID.Valid {
		resp.Booking = &BookingSummary{
			ID:          uuid.UUID(row.BookingID.Bytes).String(),
			Status:      row.BookingStatus.String,
			ConfirmedAt: row.BookingConfirmedAt.Time,
		}
	}

	if row.RefundID.Valid {
		resp.Refund = &RefundSummary{
			ID:            uuid.UUID(row.RefundID.Bytes).String(),
			Status:        row.RefundStatus,
			AmountMinor:   row.RefundAmountMinor,
			Currency:      row.RefundCurrency,
			FailureReason: textPtr(row.RefundFailureReason),
			CreatedAt:     row.RefundCreatedAt.Time,
			CompletedAt:   timePtr(row.RefundCompletedAt),
		}
	}

	return resp, nil
}

func deriveReservationOutcome(
	reservationStatus string,
	paymentStatus string,
	refundStatus string,
) (ReservationOutcome, bool, error) {
	// 1. Refund present: must only occur when payment succeeded and reservation is not held.
	if refundStatus != "" {
		if paymentStatus != "succeeded" || reservationStatus == "held" {
			return "", false, fmt.Errorf(
				"%w: refund_status=%s with reservation_status=%s payment_status=%s",
				ErrInconsistentReservationState,
				refundStatus,
				reservationStatus,
				paymentStatus,
			)
		}

		switch reservationStatus {
		case "confirmed", "expired", "cancelled":
		default:
			return "", false, fmt.Errorf(
				"%w: unknown reservation_status=%s",
				ErrInconsistentReservationState,
				reservationStatus,
			)
		}

		switch refundStatus {
		case "pending", "processing":
			return OutcomeRefundPending, false, nil
		case "succeeded":
			return OutcomeRefunded, true, nil
		case "failed":
			return OutcomeRefundFailed, true, nil
		default:
			return "", false, fmt.Errorf(
				"%w: unknown refund_status=%s",
				ErrInconsistentReservationState,
				refundStatus,
			)
		}
	}

	// 2. Successful payment without a refund record yet.
	if paymentStatus == "succeeded" {
		switch reservationStatus {
		case "confirmed":
			return OutcomeConfirmed, true, nil
		case "expired", "cancelled":
			return OutcomeRefundPending, false, nil
		case "held":
			return "", false, fmt.Errorf(
				"%w: payment succeeded while reservation is still held",
				ErrInconsistentReservationState,
			)
		default:
			return "", false, fmt.Errorf(
				"%w: unknown reservation_status=%s",
				ErrInconsistentReservationState,
				reservationStatus,
			)
		}
	}

	// 3. No succeeded payment (unpaid, initializing, pending, failed, or expired).
	switch paymentStatus {
	case "", "initializing", "pending", "failed", "expired":
	default:
		return "", false, fmt.Errorf(
			"%w: unknown payment_status=%s",
			ErrInconsistentReservationState,
			paymentStatus,
		)
	}

	switch reservationStatus {
	case "held":
		return OutcomePending, false, nil
	case "expired":
		return OutcomeExpired, true, nil
	case "cancelled":
		return OutcomeCancelled, true, nil
	case "confirmed":
		return "", false, fmt.Errorf(
			"%w: reservation confirmed without a succeeded payment",
			ErrInconsistentReservationState,
		)
	default:
		return "", false, fmt.Errorf(
			"%w: unknown reservation_status=%s",
			ErrInconsistentReservationState,
			reservationStatus,
		)
	}
}

func textPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

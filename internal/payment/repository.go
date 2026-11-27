package payment

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
		return nil, errors.New("payment: database pool cannot be nil")
	}

	return &Repository{pool: pool}, nil
}

func (r *Repository) Pool() *pgxpool.Pool {
	return r.pool
}

func (r *Repository) GetReservationForUpdateTx(
	ctx context.Context,
	tx pgx.Tx,
	id pgtype.UUID,
) (sqlc.Reservation, error) {
	res, err := sqlc.New(tx).GetReservationForUpdate(ctx, id)
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf("payment: lock reservation: %w", err)
	}
	return res, nil
}

func (r *Repository) GetReservationLifecycleReferenceTx(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (sqlc.GetReservationLifecycleReferenceRow, error) {
	ref, err := sqlc.New(tx).GetReservationLifecycleReference(ctx, reservationID)
	if err != nil {
		return sqlc.GetReservationLifecycleReferenceRow{}, fmt.Errorf("payment: get reservation lifecycle ref: %w", err)
	}
	return ref, nil
}

func (r *Repository) LockEventForReservationLifecycleTx(
	ctx context.Context,
	tx pgx.Tx,
	eventID pgtype.UUID,
) (sqlc.LockEventForReservationLifecycleRow, error) {
	ev, err := sqlc.New(tx).LockEventForReservationLifecycle(ctx, eventID)
	if err != nil {
		return sqlc.LockEventForReservationLifecycleRow{}, fmt.Errorf("payment: lock event: %w", err)
	}
	return ev, nil
}

func (r *Repository) GetActivePaymentAttemptForReservationTx(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).GetActivePaymentAttemptForReservation(ctx, reservationID)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: get active payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) CreatePaymentAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.CreatePaymentAttemptParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).CreatePaymentAttempt(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: create payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) ReclaimPaymentAttemptInitLeaseTx(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.ReclaimPaymentAttemptInitLeaseParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).ReclaimPaymentAttemptInitLease(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: reclaim init lease: %w", err)
	}
	return attempt, nil
}

func (r *Repository) ActivatePaymentAttemptSessionTx(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.ActivatePaymentAttemptSessionParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).ActivatePaymentAttemptSession(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: activate payment attempt session: %w", err)
	}
	return attempt, nil
}

func (r *Repository) FailInitializingPaymentAttempt(
	ctx context.Context,
	arg sqlc.FailInitializingPaymentAttemptParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(r.pool).FailInitializingPaymentAttempt(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: fail initializing payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) ExpirePaymentAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	id pgtype.UUID,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).ExpirePaymentAttempt(ctx, id)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: expire payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) SucceedPaymentAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.SucceedPaymentAttemptParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).SucceedPaymentAttempt(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: succeed payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) FailPaymentAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.FailPaymentAttemptParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).FailPaymentAttempt(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: fail payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) GetPaymentAttemptByID(
	ctx context.Context,
	id pgtype.UUID,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(r.pool).GetPaymentAttemptByID(ctx, id)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: get payment attempt by ID: %w", err)
	}
	return attempt, nil
}

func (r *Repository) LockPaymentAttemptByIDTx(
	ctx context.Context,
	tx pgx.Tx,
	id pgtype.UUID,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).LockPaymentAttemptByID(ctx, id)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: lock payment attempt by ID: %w", err)
	}
	return attempt, nil
}

func (r *Repository) GetPaymentAttemptByProviderOrderTx(
	ctx context.Context,
	tx pgx.Tx,
	provider string,
	orderRef string,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).GetPaymentAttemptByProviderOrder(ctx, sqlc.GetPaymentAttemptByProviderOrderParams{
		Provider: provider,
		ProviderOrderReference: pgtype.Text{
			String: orderRef,
			Valid:  true,
		},
	})
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: get payment attempt by provider order: %w", err)
	}
	return attempt, nil
}

func (r *Repository) CreatePaymentWebhookEvent(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.CreatePaymentWebhookEventParams,
) (sqlc.PaymentWebhookEvent, error) {
	event, err := sqlc.New(tx).CreatePaymentWebhookEvent(ctx, arg)
	if err != nil {
		return sqlc.PaymentWebhookEvent{}, fmt.Errorf("payment: create webhook event: %w", err)
	}
	return event, nil
}

func (r *Repository) GetPaymentWebhookEvent(
	ctx context.Context,
	tx pgx.Tx,
	provider string,
	eventID string,
) (sqlc.PaymentWebhookEvent, error) {
	event, err := sqlc.New(tx).LockPaymentWebhookEvent(ctx, sqlc.LockPaymentWebhookEventParams{
		Provider:        provider,
		ProviderEventID: eventID,
	})
	if err != nil {
		return sqlc.PaymentWebhookEvent{}, fmt.Errorf("payment: lock webhook event: %w", err)
	}
	return event, nil
}

func (r *Repository) UpdatePaymentWebhookEventStatus(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.UpdatePaymentWebhookEventStatusParams,
) (sqlc.PaymentWebhookEvent, error) {
	event, err := sqlc.New(tx).UpdatePaymentWebhookEventStatus(ctx, arg)
	if err != nil {
		return sqlc.PaymentWebhookEvent{}, fmt.Errorf("payment: update webhook event status: %w", err)
	}
	return event, nil
}

func (r *Repository) SumActiveRefundsForAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	attemptID pgtype.UUID,
) (int64, error) {
	total, err := sqlc.New(tx).SumActiveRefundsForAttempt(ctx, attemptID)
	if err != nil {
		return 0, fmt.Errorf("payment: sum active refunds: %w", err)
	}
	return total, nil
}

func (r *Repository) CreateRefundTx(
	ctx context.Context,
	tx pgx.Tx,
	arg sqlc.CreateRefundParams,
) (sqlc.Refund, error) {
	refund, err := sqlc.New(tx).CreateRefund(ctx, arg)
	if err != nil {
		return sqlc.Refund{}, fmt.Errorf("payment: create refund: %w", err)
	}
	return refund, nil
}

func (r *Repository) GetRefundByPaymentAttemptIDTx(
	ctx context.Context,
	tx pgx.Tx,
	attemptID pgtype.UUID,
) (sqlc.Refund, error) {
	refund, err := sqlc.New(tx).GetRefundByPaymentAttemptID(ctx, attemptID)
	if err != nil {
		return sqlc.Refund{}, fmt.Errorf("payment: get refund by payment attempt id: %w", err)
	}
	return refund, nil
}

func (r *Repository) GetBookingByReservationIDTx(
	ctx context.Context,
	tx pgx.Tx,
	reservationID pgtype.UUID,
) (sqlc.Booking, error) {
	booking, err := sqlc.New(tx).GetBookingByReservationID(ctx, reservationID)
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("payment: get booking by reservation id: %w", err)
	}
	return booking, nil
}

func (r *Repository) ClaimPendingRefunds(
	ctx context.Context,
	limitCount int32,
	leaseDuration pgtype.Interval,
) ([]sqlc.Refund, error) {
	refunds, err := sqlc.New(r.pool).ClaimPendingRefunds(ctx, sqlc.ClaimPendingRefundsParams{
		LimitCount:    limitCount,
		LeaseDuration: leaseDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("payment: claim pending refunds: %w", err)
	}
	return refunds, nil
}

func (r *Repository) CompleteRefund(
	ctx context.Context,
	id pgtype.UUID,
	claimToken pgtype.UUID,
	providerRef string,
) (bool, error) {
	rows, err := sqlc.New(r.pool).CompleteRefund(ctx, sqlc.CompleteRefundParams{
		ID:                      id,
		ClaimToken:              claimToken,
		ProviderRefundReference: pgtype.Text{String: providerRef, Valid: providerRef != ""},
	})
	if err != nil {
		return false, fmt.Errorf("payment: complete refund: %w", err)
	}
	return rows == 1, nil
}

func (r *Repository) RetryRefund(
	ctx context.Context,
	id pgtype.UUID,
	claimToken pgtype.UUID,
	nextAttemptAt pgtype.Timestamptz,
	lastError string,
) (bool, error) {
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}
	rows, err := sqlc.New(r.pool).RetryRefund(ctx, sqlc.RetryRefundParams{
		ID:            id,
		ClaimToken:    claimToken,
		NextAttemptAt: nextAttemptAt,
		LastError:     pgtype.Text{String: lastError, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("payment: retry refund: %w", err)
	}
	return rows == 1, nil
}

func (r *Repository) FailRefund(
	ctx context.Context,
	id pgtype.UUID,
	claimToken pgtype.UUID,
	deliveryAttempted bool,
	lastError string,
) (bool, error) {
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}
	rows, err := sqlc.New(r.pool).FailRefund(ctx, sqlc.FailRefundParams{
		ID:                id,
		ClaimToken:        claimToken,
		DeliveryAttempted: deliveryAttempted,
		FailureReason:     pgtype.Text{String: lastError, Valid: true},
		LastError:         pgtype.Text{String: lastError, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("payment: fail refund: %w", err)
	}
	return rows == 1, nil
}

func (r *Repository) RetryFailedRefund(ctx context.Context, id pgtype.UUID) (sqlc.Refund, error) {
	refund, err := sqlc.New(r.pool).RetryFailedRefund(ctx, id)
	if err != nil {
		return sqlc.Refund{}, fmt.Errorf("payment: retry failed refund: %w", err)
	}
	return refund, nil
}

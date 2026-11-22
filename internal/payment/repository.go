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

func (r *Repository) GetReservationByIDForCustomer(
	ctx context.Context,
	id pgtype.UUID,
	customerID pgtype.UUID,
) (sqlc.Reservation, error) {
	res, err := sqlc.New(r.pool).GetReservationByIDForCustomer(ctx, sqlc.GetReservationByIDForCustomerParams{
		ID:         id,
		CustomerID: customerID,
	})
	if err != nil {
		return sqlc.Reservation{}, fmt.Errorf("payment: get reservation: %w", err)
	}
	return res, nil
}

func (r *Repository) CreatePaymentAttempt(
	ctx context.Context,
	arg sqlc.CreatePaymentAttemptParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(r.pool).CreatePaymentAttempt(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: create payment attempt: %w", err)
	}
	return attempt, nil
}

func (r *Repository) UpdatePaymentAttemptProviderOrder(
	ctx context.Context,
	arg sqlc.UpdatePaymentAttemptProviderOrderParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(r.pool).UpdatePaymentAttemptProviderOrder(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: update payment attempt provider order: %w", err)
	}
	return attempt, nil
}

func (r *Repository) FailPaymentAttempt(
	ctx context.Context,
	arg sqlc.FailPaymentAttemptParams,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(r.pool).FailPaymentAttempt(ctx, arg)
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: fail payment attempt: %w", err)
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

func (r *Repository) GetPaymentAttemptByProviderOrder(
	ctx context.Context,
	provider string,
	orderRef string,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(r.pool).GetPaymentAttemptByProviderOrder(ctx, sqlc.GetPaymentAttemptByProviderOrderParams{
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

func (r *Repository) GetPaymentAttemptByProviderOrderTx(
	ctx context.Context,
	tx pgx.Tx,
	provider string,
	orderRef string,
) (sqlc.PaymentAttempt, error) {
	attempt, err := sqlc.New(tx).LockPaymentAttemptByProviderOrder(ctx, sqlc.LockPaymentAttemptByProviderOrderParams{
		Provider: provider,
		ProviderOrderReference: pgtype.Text{
			String: orderRef,
			Valid:  true,
		},
	})
	if err != nil {
		return sqlc.PaymentAttempt{}, fmt.Errorf("payment: lock payment attempt by provider order tx: %w", err)
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

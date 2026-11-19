package idempotency

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Punith1117/ticket-forge/internal/database/sqlc"
)

type repository struct{}

func newRepository() *repository {
	return &repository{}
}

func (r *repository) create(
	ctx context.Context,
	tx pgx.Tx,
	userID pgtype.UUID,
	operation string,
	key string,
	requestHash string,
) (sqlc.IdempotencyRecord, error) {
	q := sqlc.New(tx)

	return q.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
		UserID:         userID,
		Operation:      operation,
		IdempotencyKey: key,
		RequestHash:    requestHash,
	})
}

func (r *repository) get(
	ctx context.Context,
	tx pgx.Tx,
	userID pgtype.UUID,
	operation string,
	key string,
) (sqlc.IdempotencyRecord, error) {
	q := sqlc.New(tx)

	return q.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		UserID:         userID,
		Operation:      operation,
		IdempotencyKey: key,
	})
}

func (r *repository) complete(
	ctx context.Context,
	tx pgx.Tx,
	recordID pgtype.UUID,
	status int,
	body []byte,
) error {
	q := sqlc.New(tx)

	_, err := q.CompleteIdempotencyRecord(
		ctx,
		sqlc.CompleteIdempotencyRecordParams{
			ID: recordID,
			ResponseStatus: pgtype.Int4{
				Int32: int32(status),
				Valid: true,
			},
			ResponseBody: body,
		},
	)

	if err == pgx.ErrNoRows {
		return ErrAlreadyCompleted
	}

	return err
}

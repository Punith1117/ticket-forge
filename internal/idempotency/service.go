package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Service struct {
	repository *repository
}

func NewService() *Service {
	return &Service{
		repository: newRepository(),
	}
}

type Request struct {
	UserID    string
	Operation string
	Key       string
	Payload   []byte
}

type Response struct {
	StatusCode int
	Body       []byte
}

type Execution struct {
	RecordID pgtype.UUID
	Replay   *Response
}

func (s *Service) Begin(
	ctx context.Context,
	tx pgx.Tx,
	req Request,
) (Execution, error) {
	if tx == nil ||
		strings.TrimSpace(req.Operation) == "" ||
		strings.TrimSpace(req.Key) == "" {
		return Execution{}, ErrInvalidRequest
	}

	var userID pgtype.UUID
	if err := userID.Scan(req.UserID); err != nil {
		return Execution{}, ErrInvalidRequest
	}

	var idempotencyKey pgtype.UUID
	if err := idempotencyKey.Scan(req.Key); err != nil {
		return Execution{}, ErrInvalidRequest
	}

	key := idempotencyKey.String()

	hash := sha256.Sum256(req.Payload)
	requestHash := hex.EncodeToString(hash[:])

	record, err := s.repository.create(
		ctx,
		tx,
		userID,
		req.Operation,
		key,
		requestHash,
	)

	if err == nil {
		return Execution{
			RecordID: record.ID,
		}, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, err
	}

	record, err = s.repository.get(
		ctx,
		tx,
		userID,
		req.Operation,
		key,
	)
	if err != nil {
		return Execution{}, err
	}

	if record.RequestHash != requestHash {
		return Execution{}, ErrKeyReused
	}

	if !record.ResponseStatus.Valid || record.ResponseBody == nil {
		return Execution{}, ErrIncompleteRecord
	}

	return Execution{
		Replay: &Response{
			StatusCode: int(record.ResponseStatus.Int32),
			Body:       record.ResponseBody,
		},
	}, nil
}

func (s *Service) Complete(
	ctx context.Context,
	tx pgx.Tx,
	execution Execution,
	statusCode int,
	body []byte,
) error {
	if tx == nil ||
		!execution.RecordID.Valid ||
		statusCode < 200 ||
		statusCode >= 300 ||
		!json.Valid(body) {
		return ErrInvalidRequest
	}

	return s.repository.complete(
		ctx,
		tx,
		execution.RecordID,
		statusCode,
		body,
	)
}

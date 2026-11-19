package idempotency

import "errors"

var (
	ErrInvalidRequest   = errors.New("invalid idempotency request")
	ErrKeyReused        = errors.New("idempotency key reused with a different request")
	ErrIncompleteRecord = errors.New("idempotency record is incomplete")
	ErrAlreadyCompleted = errors.New("idempotency record already completed")
)

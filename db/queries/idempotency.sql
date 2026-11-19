-- name: CreateIdempotencyRecord :one
INSERT INTO idempotency_records (
    user_id,
    operation,
    idempotency_key,
    request_hash
)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, operation, idempotency_key)
DO NOTHING
RETURNING *;


-- name: GetIdempotencyRecord :one
SELECT *
FROM idempotency_records
WHERE user_id = $1
  AND operation = $2
  AND idempotency_key = $3;


-- name: CompleteIdempotencyRecord :one
UPDATE idempotency_records
SET
    response_status = $2,
    response_body = $3
WHERE id = $1
  AND response_status IS NULL
  AND response_body IS NULL
RETURNING *;

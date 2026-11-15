-- name: ClaimOutboxEvents :many
WITH eligible AS (
    SELECT id
    FROM outbox_events
    WHERE event_type = ANY(@event_types::text[])
      AND (
          (status = 'pending' AND next_attempt_at <= now())
          OR
          (status = 'processing' AND claim_expires_at <= now())
      )
    ORDER BY next_attempt_at ASC NULLS FIRST, occurred_at ASC
    LIMIT @limit_count
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events
SET status = 'processing',
    claim_token = uuidv7(),
    claimed_at = now(),
    claim_expires_at = now() + @lease_duration::interval
FROM eligible
WHERE outbox_events.id = eligible.id
RETURNING outbox_events.*;

-- name: CompleteOutboxEvent :execrows
UPDATE outbox_events
SET status = 'published',
    published_at = now(),
    processed_at = now(),
    provider_message_id = @provider_message_id,
    payload = payload - 'encrypted_token',
    last_error = NULL
WHERE id = @id
  AND status = 'processing'
  AND claim_token = @claim_token;

-- name: RetryOutboxEvent :execrows
UPDATE outbox_events
SET status = 'pending',
    next_attempt_at = @next_attempt_at,
    attempts = attempts + 1,
    last_attempt_at = now(),
    last_error = @last_error,
    claim_token = NULL,
    claim_expires_at = NULL
WHERE id = @id
  AND status = 'processing'
  AND claim_token = @claim_token;

-- name: FailOutboxEvent :execrows
UPDATE outbox_events
SET status = 'failed',
    processed_at = now(),
    attempts = CASE WHEN @delivery_attempted::boolean THEN attempts + 1 ELSE attempts END,
    last_attempt_at = CASE WHEN @delivery_attempted::boolean THEN now() ELSE last_attempt_at END,
    last_error = @last_error,
    claim_token = NULL,
    claim_expires_at = NULL,
    payload = payload - 'encrypted_token'
WHERE id = @id
  AND status = 'processing'
  AND claim_token = @claim_token;

-- name: InsertOutboxEvent :one
INSERT INTO outbox_events (
    aggregate_type,
    aggregate_id,
    event_type,
    payload
) VALUES (
    @aggregate_type,
    @aggregate_id,
    @event_type,
    @payload
)
RETURNING *;

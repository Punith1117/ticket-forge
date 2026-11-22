-- ============================================================
-- PAYMENT ATTEMPTS
-- ============================================================

-- name: CreatePaymentAttempt :one
INSERT INTO payment_attempts (
    id,
    reservation_id,
    amount_minor,
    currency,
    status,
    provider,
    provider_order_reference,
    attempted_at
)
VALUES (
    @id,
    @reservation_id,
    @amount_minor,
    @currency,
    'pending',
    @provider,
    @provider_order_reference,
    clock_timestamp()
)
RETURNING *;

-- name: UpdatePaymentAttemptProviderOrder :one
UPDATE payment_attempts
SET
    provider_order_reference = @provider_order_reference
WHERE id = @id
RETURNING *;

-- name: FailPaymentAttempt :one
UPDATE payment_attempts
SET
    status = 'failed',
    failure_reason = @failure_reason,
    completed_at = clock_timestamp()
WHERE id = @id
  AND status = 'pending'
RETURNING *;

-- name: SucceedPaymentAttempt :one
UPDATE payment_attempts
SET
    status = 'succeeded',
    provider_reference = @provider_reference,
    completed_at = clock_timestamp()
WHERE id = @id
  AND status = 'pending'
RETURNING *;

-- name: GetPaymentAttemptByID :one
SELECT *
FROM payment_attempts
WHERE id = @id
LIMIT 1;

-- name: GetPaymentAttemptByProviderOrder :one
SELECT *
FROM payment_attempts
WHERE provider = @provider
  AND provider_order_reference = @provider_order_reference
LIMIT 1;

-- name: LockPaymentAttemptByProviderOrder :one
SELECT *
FROM payment_attempts
WHERE provider = @provider
  AND provider_order_reference = @provider_order_reference
FOR UPDATE;

-- ============================================================
-- WEBHOOK EVENTS
-- ============================================================

-- name: CreatePaymentWebhookEvent :one
INSERT INTO payment_webhook_events (
    id,
    provider,
    provider_event_id,
    payment_attempt_id,
    event_type,
    payload,
    processing_status,
    received_at,
    processed_at
)
VALUES (
    @id,
    @provider,
    @provider_event_id,
    @payment_attempt_id,
    @event_type,
    @payload,
    @processing_status,
    clock_timestamp(),
    CASE
        WHEN @processing_status = 'processed' THEN clock_timestamp()
        ELSE NULL
    END
)
ON CONFLICT (provider, provider_event_id) DO NOTHING
RETURNING *;

-- name: GetPaymentWebhookEvent :one
SELECT *
FROM payment_webhook_events
WHERE provider = @provider
  AND provider_event_id = @provider_event_id
LIMIT 1;

-- name: LockPaymentWebhookEvent :one
SELECT *
FROM payment_webhook_events
WHERE provider = @provider
  AND provider_event_id = @provider_event_id
FOR UPDATE;

-- name: UpdatePaymentWebhookEventStatus :one
UPDATE payment_webhook_events
SET
    payment_attempt_id = COALESCE(@payment_attempt_id, payment_attempt_id),
    processing_status = @processing_status,
    processed_at = CASE
        WHEN @processing_status = 'processed' THEN clock_timestamp()
        ELSE NULL
    END
WHERE id = @id
RETURNING *;

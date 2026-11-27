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
    init_claim_token,
    init_lease_expires_at,
    attempted_at
)
VALUES (
    @id,
    @reservation_id,
    @amount_minor,
    @currency,
    'initializing',
    @provider,
    @init_claim_token,
    clock_timestamp() + @lease_duration::interval,
    clock_timestamp()
)
RETURNING *;

-- name: GetActivePaymentAttemptForReservation :one
SELECT *
FROM payment_attempts
WHERE reservation_id = @reservation_id
  AND status IN ('initializing', 'pending')
LIMIT 1;

-- name: ReclaimPaymentAttemptInitLease :one
UPDATE payment_attempts
SET
    init_claim_token = @init_claim_token,
    init_lease_expires_at = clock_timestamp() + @lease_duration::interval
WHERE id = @id
  AND status = 'initializing'
  AND init_lease_expires_at <= clock_timestamp()
RETURNING *;

-- name: ActivatePaymentAttemptSession :one
UPDATE payment_attempts
SET
    status = 'pending',
    provider_checkout_session_id = @provider_checkout_session_id,
    provider_order_reference = @provider_order_reference,
    checkout_url = @checkout_url,
    expires_at = @expires_at,
    init_claim_token = NULL,
    init_lease_expires_at = NULL
WHERE id = @id
  AND status = 'initializing'
  AND init_claim_token = @init_claim_token
RETURNING *;

-- name: FailInitializingPaymentAttempt :one
UPDATE payment_attempts
SET
    status = 'failed',
    failure_reason = @failure_reason,
    completed_at = clock_timestamp(),
    init_claim_token = NULL,
    init_lease_expires_at = NULL
WHERE id = @id
  AND status = 'initializing'
  AND init_claim_token = @init_claim_token
RETURNING *;

-- name: ExpireInitializingPaymentAttempt :one
UPDATE payment_attempts
SET
    status = 'expired',
    completed_at = clock_timestamp(),
    init_claim_token = NULL,
    init_lease_expires_at = NULL
WHERE id = @id
  AND status = 'initializing'
  AND init_claim_token = @init_claim_token
RETURNING *;

-- name: ExpirePaymentAttempt :one
UPDATE payment_attempts
SET
    status = 'expired',
    completed_at = clock_timestamp()
WHERE id = @id
  AND status = 'pending'
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
  AND status IN ('pending', 'expired')
RETURNING *;

-- name: GetPaymentAttemptByID :one
SELECT *
FROM payment_attempts
WHERE id = @id
LIMIT 1;

-- name: LockPaymentAttemptByID :one
SELECT *
FROM payment_attempts
WHERE id = @id
FOR UPDATE;

-- name: GetPaymentAttemptByProviderOrder :one
SELECT *
FROM payment_attempts
WHERE provider = @provider
  AND provider_order_reference = @provider_order_reference
LIMIT 1;

-- name: GetPaymentAttemptByProviderSession :one
SELECT *
FROM payment_attempts
WHERE provider = @provider
  AND provider_checkout_session_id = @provider_checkout_session_id
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
        WHEN @processing_status IN ('processed', 'ignored') THEN clock_timestamp()
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
        WHEN @processing_status IN ('processed', 'ignored') THEN clock_timestamp()
        ELSE NULL
    END
WHERE id = @id
RETURNING *;

-- ============================================================
-- REFUNDS
-- ============================================================

-- name: SumActiveRefundsForAttempt :one
SELECT COALESCE(SUM(amount_minor), 0)::bigint AS total_amount_minor
FROM refunds
WHERE payment_attempt_id = @payment_attempt_id
  AND status IN ('pending', 'processing', 'succeeded');

-- name: CreateRefund :one
INSERT INTO refunds (
    id,
    payment_attempt_id,
    booking_id,
    amount_minor,
    currency,
    status,
    idempotency_key
) VALUES (
    @id,
    @payment_attempt_id,
    @booking_id,
    @amount_minor,
    @currency,
    'pending',
    @idempotency_key
)
ON CONFLICT (payment_attempt_id, idempotency_key) WHERE idempotency_key <> '' DO NOTHING
RETURNING *;

-- name: GetRefundByID :one
SELECT *
FROM refunds
WHERE id = @id
LIMIT 1;

-- name: GetRefundByPaymentAttemptID :one
SELECT *
FROM refunds
WHERE payment_attempt_id = @payment_attempt_id
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ClaimPendingRefunds :many
WITH eligible AS (
    SELECT id
    FROM refunds
    WHERE (
        (status = 'pending' AND next_attempt_at <= clock_timestamp())
        OR
        (status = 'processing' AND claim_expires_at <= clock_timestamp())
    )
    ORDER BY next_attempt_at ASC, created_at ASC
    LIMIT @limit_count
    FOR UPDATE SKIP LOCKED
)
UPDATE refunds
SET
    status = 'processing',
    claim_token = uuidv7(),
    claim_expires_at = clock_timestamp() + @lease_duration::interval
FROM eligible
WHERE refunds.id = eligible.id
RETURNING refunds.*;

-- name: CompleteRefund :execrows
UPDATE refunds
SET
    status = 'succeeded',
    provider_refund_reference = @provider_refund_reference,
    completed_at = clock_timestamp(),
    last_attempt_at = clock_timestamp(),
    last_error = NULL,
    claim_token = NULL,
    claim_expires_at = NULL
WHERE id = @id
  AND status = 'processing'
  AND claim_token = @claim_token;

-- name: RetryRefund :execrows
UPDATE refunds
SET
    status = 'pending',
    next_attempt_at = @next_attempt_at,
    attempts = attempts + 1,
    last_attempt_at = clock_timestamp(),
    last_error = @last_error,
    claim_token = NULL,
    claim_expires_at = NULL
WHERE id = @id
  AND status = 'processing'
  AND claim_token = @claim_token;

-- name: FailRefund :execrows
UPDATE refunds
SET
    status = 'failed',
    failure_reason = @failure_reason,
    attempts = CASE WHEN @delivery_attempted::boolean THEN attempts + 1 ELSE attempts END,
    last_attempt_at = CASE WHEN @delivery_attempted::boolean THEN clock_timestamp() ELSE last_attempt_at END,
    last_error = @last_error,
    claim_token = NULL,
    claim_expires_at = NULL
WHERE id = @id
  AND status = 'processing'
  AND claim_token = @claim_token;

-- name: RetryFailedRefund :one
UPDATE refunds
SET
    status = 'pending',
    next_attempt_at = clock_timestamp(),
    failure_reason = NULL,
    last_error = NULL
WHERE id = @id
  AND status = 'failed'
RETURNING *;

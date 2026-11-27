-- +goose Up
-- +goose StatementBegin

-- 1. payment_attempts: durable initialization lifecycle, session metadata, and
--    at most one active (initializing or pending) attempt per reservation.
ALTER TABLE payment_attempts
    DROP CONSTRAINT IF EXISTS chk_payment_attempts_status;

ALTER TABLE payment_attempts
    ADD COLUMN provider_checkout_session_id text,
    ADD COLUMN checkout_url text,
    ADD COLUMN expires_at timestamptz,
    ADD COLUMN init_claim_token uuid,
    ADD COLUMN init_lease_expires_at timestamptz;

ALTER TABLE payment_attempts
    ADD CONSTRAINT chk_payment_attempts_status
        CHECK (status IN ('initializing', 'pending', 'succeeded', 'failed', 'expired')),
    ADD CONSTRAINT chk_payment_attempts_init_lease
        CHECK (
            (status = 'initializing' AND init_claim_token IS NOT NULL AND init_lease_expires_at IS NOT NULL)
            OR
            (status <> 'initializing' AND init_claim_token IS NULL AND init_lease_expires_at IS NULL)
        );

-- Expire any historical duplicate pending attempts per reservation before adding
-- the partial unique index, keeping only the most recent pending attempt.
WITH ranked_pending AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY reservation_id
            ORDER BY attempted_at DESC, id DESC
        ) AS rn
    FROM payment_attempts
    WHERE status = 'pending'
)
UPDATE payment_attempts AS pa
SET
    status = 'expired',
    completed_at = COALESCE(pa.completed_at, clock_timestamp())
FROM ranked_pending AS rp
WHERE pa.id = rp.id
  AND rp.rn > 1;

CREATE UNIQUE INDEX uq_payment_attempts_one_active_per_reservation
    ON payment_attempts (reservation_id)
    WHERE status IN ('initializing', 'pending');

CREATE UNIQUE INDEX uq_payment_attempts_provider_session
    ON payment_attempts (provider, provider_checkout_session_id)
    WHERE provider_checkout_session_id IS NOT NULL;


-- 2. bookings: explicit reference to the settling payment attempt.
ALTER TABLE bookings
    ADD COLUMN payment_attempt_id uuid
        REFERENCES payment_attempts(id) ON DELETE RESTRICT;

UPDATE bookings AS b
SET payment_attempt_id = (
    SELECT p.id
    FROM payment_attempts AS p
    WHERE p.reservation_id = b.reservation_id
      AND p.status = 'succeeded'
    ORDER BY p.completed_at DESC NULLS LAST, p.attempted_at DESC, p.id DESC
    LIMIT 1
)
WHERE b.payment_attempt_id IS NULL;

CREATE UNIQUE INDEX uq_bookings_payment_attempt_id
    ON bookings (payment_attempt_id)
    WHERE payment_attempt_id IS NOT NULL;

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS uq_bookings_payment_attempt_id;

ALTER TABLE bookings
    DROP COLUMN IF EXISTS payment_attempt_id;

DROP INDEX IF EXISTS uq_payment_attempts_provider_session;
DROP INDEX IF EXISTS uq_payment_attempts_one_active_per_reservation;

ALTER TABLE payment_attempts
    DROP CONSTRAINT IF EXISTS chk_payment_attempts_init_lease,
    DROP CONSTRAINT IF EXISTS chk_payment_attempts_status;

UPDATE payment_attempts
SET
    status = 'failed',
    init_claim_token = NULL,
    init_lease_expires_at = NULL,
    completed_at = COALESCE(completed_at, clock_timestamp())
WHERE status IN ('initializing', 'expired');

ALTER TABLE payment_attempts
    DROP COLUMN IF EXISTS init_lease_expires_at,
    DROP COLUMN IF EXISTS init_claim_token,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS checkout_url,
    DROP COLUMN IF EXISTS provider_checkout_session_id;

ALTER TABLE payment_attempts
    ADD CONSTRAINT chk_payment_attempts_status
        CHECK (status IN ('pending', 'succeeded', 'failed'));

-- +goose StatementEnd

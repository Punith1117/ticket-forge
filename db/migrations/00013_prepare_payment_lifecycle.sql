-- +goose Up
-- +goose StatementBegin

-- Ensure every ticket category has an inventory row.
-- Existing inventory rows are preserved.
INSERT INTO inventory (ticket_category_id)
SELECT tc.id
FROM ticket_categories AS tc
LEFT JOIN inventory AS i
    ON i.ticket_category_id = tc.id
WHERE i.ticket_category_id IS NULL
ON CONFLICT (ticket_category_id) DO NOTHING;


-- Identify the provider and its checkout/order reference.
-- provider_reference remains the provider's actual payment reference.
ALTER TABLE payment_attempts
    ADD COLUMN provider text NOT NULL,
    ADD COLUMN provider_order_reference text;

ALTER TABLE payment_attempts
    ADD CONSTRAINT chk_payment_attempts_provider
        CHECK (length(trim(provider)) > 0);

CREATE UNIQUE INDEX uq_payment_attempts_provider_order
    ON payment_attempts (provider, provider_order_reference)
    WHERE provider_order_reference IS NOT NULL;

CREATE UNIQUE INDEX uq_payment_attempts_provider_payment
    ON payment_attempts (provider, provider_reference)
    WHERE provider_reference IS NOT NULL;


-- A refund belongs to a payment attempt.
-- booking_id is optional because a late successful payment may never
-- produce a booking.
ALTER TABLE refunds
    ALTER COLUMN booking_id DROP NOT NULL;

ALTER TABLE refunds
    ADD COLUMN payment_attempt_id uuid NOT NULL
        REFERENCES payment_attempts(id) ON DELETE RESTRICT;

ALTER TABLE refunds
    ADD CONSTRAINT uq_refunds_payment_attempt
        UNIQUE (payment_attempt_id);


-- Give the refund worker durable claiming and retry information.
ALTER TABLE refunds
    DROP CONSTRAINT chk_refunds_status;

ALTER TABLE refunds
    ADD COLUMN provider_refund_reference text,
    ADD COLUMN attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN claim_token uuid,
    ADD COLUMN claim_expires_at timestamptz,
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN last_error text;

ALTER TABLE refunds
    ADD CONSTRAINT chk_refunds_status
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed')),
    ADD CONSTRAINT chk_refunds_attempts
        CHECK (attempts >= 0),
    ADD CONSTRAINT chk_refunds_claim
        CHECK (
            (status = 'processing'
                AND claim_token IS NOT NULL
                AND claim_expires_at IS NOT NULL)
            OR
            (status <> 'processing')
        ),
    ADD CONSTRAINT chk_refunds_last_error
        CHECK (last_error IS NULL OR length(last_error) <= 2000);

CREATE INDEX idx_refunds_pending
    ON refunds (next_attempt_at, created_at)
    WHERE status = 'pending';

CREATE INDEX idx_refunds_expired_leases
    ON refunds (claim_expires_at)
    WHERE status = 'processing';

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin

-- Revert refund worker lifecycle fields.
-- A processing refund is returned to pending before restoring the old
-- status constraint.
UPDATE refunds
SET status = 'pending',
    claim_token = NULL,
    claim_expires_at = NULL
WHERE status = 'processing';

DROP INDEX IF EXISTS idx_refunds_expired_leases;
DROP INDEX IF EXISTS idx_refunds_pending;

ALTER TABLE refunds
    DROP CONSTRAINT IF EXISTS chk_refunds_last_error,
    DROP CONSTRAINT IF EXISTS chk_refunds_claim,
    DROP CONSTRAINT IF EXISTS chk_refunds_attempts,
    DROP CONSTRAINT IF EXISTS chk_refunds_status;

ALTER TABLE refunds
    ADD CONSTRAINT chk_refunds_status
        CHECK (status IN ('pending', 'succeeded', 'failed'));

ALTER TABLE refunds
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS last_attempt_at,
    DROP COLUMN IF EXISTS claim_expires_at,
    DROP COLUMN IF EXISTS claim_token,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS provider_refund_reference;

ALTER TABLE refunds
    DROP CONSTRAINT IF EXISTS uq_refunds_payment_attempt,
    DROP COLUMN IF EXISTS payment_attempt_id;

ALTER TABLE refunds
    ALTER COLUMN booking_id SET NOT NULL;


-- Revert payment provider identifiers.
DROP INDEX IF EXISTS uq_payment_attempts_provider_payment;
DROP INDEX IF EXISTS uq_payment_attempts_provider_order;

ALTER TABLE payment_attempts
    DROP CONSTRAINT IF EXISTS chk_payment_attempts_provider,
    DROP COLUMN IF EXISTS provider_order_reference,
    DROP COLUMN IF EXISTS provider;


-- Inventory rows inserted by this migration are intentionally retained
-- on rollback. They are valid inventory records and may have been used
-- after the migration was applied.

-- +goose StatementEnd

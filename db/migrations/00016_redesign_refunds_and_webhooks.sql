-- +goose Up
-- +goose StatementBegin

-- 1. refunds: allow multiple refunds per payment attempt (e.g. partial refunds)
--    while supporting idempotent automatic refunds via idempotency_key.
ALTER TABLE refunds
    DROP CONSTRAINT IF EXISTS uq_refunds_payment_attempt,
    DROP CONSTRAINT IF EXISTS refunds_booking_id_key;

ALTER TABLE refunds
    ADD COLUMN idempotency_key text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX uq_refunds_attempt_idempotency
    ON refunds (payment_attempt_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX idx_refunds_payment_attempt_id
    ON refunds (payment_attempt_id);


-- 2. payment_webhook_events: allow processed_at when status is 'ignored' as well as 'processed'.
ALTER TABLE payment_webhook_events
    DROP CONSTRAINT IF EXISTS chk_payment_webhook_events_processed_at;

ALTER TABLE payment_webhook_events
    ADD CONSTRAINT chk_payment_webhook_events_processed_at
        CHECK (
            (processing_status IN ('processed', 'ignored') AND processed_at IS NOT NULL)
            OR
            (processing_status NOT IN ('processed', 'ignored') AND processed_at IS NULL)
        );

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin

UPDATE payment_webhook_events
SET processed_at = NULL
WHERE processing_status = 'ignored';

ALTER TABLE payment_webhook_events
    DROP CONSTRAINT IF EXISTS chk_payment_webhook_events_processed_at;

ALTER TABLE payment_webhook_events
    ADD CONSTRAINT chk_payment_webhook_events_processed_at
        CHECK (
            (processing_status = 'processed' AND processed_at IS NOT NULL)
            OR
            (processing_status <> 'processed' AND processed_at IS NULL)
        );

DROP INDEX IF EXISTS idx_refunds_payment_attempt_id;
DROP INDEX IF EXISTS uq_refunds_attempt_idempotency;

ALTER TABLE refunds
    DROP COLUMN IF EXISTS idempotency_key;

ALTER TABLE refunds
    ADD CONSTRAINT refunds_booking_id_key UNIQUE (booking_id),
    ADD CONSTRAINT uq_refunds_payment_attempt UNIQUE (payment_attempt_id);

-- +goose StatementEnd

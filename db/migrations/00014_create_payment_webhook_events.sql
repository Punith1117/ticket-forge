-- +goose Up
-- +goose StatementBegin

CREATE TABLE payment_webhook_events (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    provider            text NOT NULL,
    provider_event_id   text NOT NULL,
    payment_attempt_id  uuid REFERENCES payment_attempts(id) ON DELETE RESTRICT,
    event_type          text NOT NULL,
    payload             jsonb NOT NULL,
    received_at         timestamptz NOT NULL DEFAULT now(),
    processed_at        timestamptz,
    processing_status   text NOT NULL DEFAULT 'processing',

    CONSTRAINT chk_webhook_events_provider
        CHECK (length(trim(provider)) > 0),

    CONSTRAINT chk_webhook_events_provider_event_id
        CHECK (length(trim(provider_event_id)) > 0),

    CONSTRAINT chk_webhook_events_status
        CHECK (processing_status IN ('processing', 'processed', 'ignored', 'failed')),

    CONSTRAINT chk_payment_webhook_events_processed_at
        CHECK (
            (processing_status = 'processed' AND processed_at IS NOT NULL)
            OR
            (processing_status <> 'processed' AND processed_at IS NULL)
        ),

    CONSTRAINT uq_payment_webhook_events_provider_event
        UNIQUE (provider, provider_event_id)
);

CREATE INDEX idx_payment_webhook_events_attempt_id
    ON payment_webhook_events (payment_attempt_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS payment_webhook_events;

-- +goose StatementEnd

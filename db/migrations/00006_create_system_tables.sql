-- +goose Up
-- +goose StatementBegin
CREATE TABLE idempotency_records (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation       text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash    text NOT NULL,
    response_status integer,
    response_body   jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,

    CONSTRAINT chk_idempotency_records_operation CHECK (length(trim(operation)) > 0),
    CONSTRAINT chk_idempotency_records_key CHECK (length(trim(idempotency_key)) > 0),
    CONSTRAINT uq_idempotency_user_op_key UNIQUE (user_id, operation, idempotency_key)
);

CREATE INDEX idx_idempotency_records_expires_at ON idempotency_records (expires_at);

CREATE TABLE outbox_events (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    aggregate_type text NOT NULL,
    aggregate_id   uuid NOT NULL,
    event_type     text NOT NULL,
    payload        jsonb NOT NULL,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz,
    attempts       integer NOT NULL DEFAULT 0,

    CONSTRAINT chk_outbox_events_attempts CHECK (attempts >= 0)
);

CREATE INDEX idx_outbox_events_unpublished ON outbox_events (occurred_at) WHERE published_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS idempotency_records;
-- +goose StatementEnd

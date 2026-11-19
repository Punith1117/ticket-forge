-- +goose Up
-- +goose StatementBegin

DROP INDEX idx_idempotency_records_expires_at;

ALTER TABLE idempotency_records
    DROP COLUMN expires_at;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE idempotency_records
    ADD COLUMN expires_at timestamptz;

CREATE INDEX idx_idempotency_records_expires_at
    ON idempotency_records (expires_at);

-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

CREATE TABLE rate_limit_buckets (
    key            text PRIMARY KEY,
    tokens         bigint NOT NULL,
    last_refill_at timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_rate_limit_buckets_key CHECK (length(trim(key)) > 0),
    CONSTRAINT chk_rate_limit_buckets_tokens CHECK (tokens >= 0)
);

CREATE INDEX idx_rate_limit_buckets_updated_at ON rate_limit_buckets (updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS rate_limit_buckets;

-- +goose StatementEnd

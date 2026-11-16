-- +goose Up
-- +goose StatementBegin

ALTER TABLE sessions
    ADD COLUMN revoked_at timestamptz;

CREATE INDEX idx_sessions_revoked_at ON sessions (revoked_at) WHERE revoked_at IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_sessions_revoked_at;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS revoked_at;

-- +goose StatementEnd

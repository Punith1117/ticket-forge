-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    email             text NOT NULL,
    password_hash     text NOT NULL,
    display_name      text NOT NULL,
    email_verified    boolean NOT NULL DEFAULT false,
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_users_email_non_empty CHECK (length(trim(email)) > 0),
    CONSTRAINT chk_users_password_hash_non_empty CHECK (length(password_hash) > 0),
    CONSTRAINT chk_users_display_name_non_empty CHECK (length(trim(display_name)) > 0)
);

CREATE UNIQUE INDEX idx_users_email_unique ON users (lower(trim(email)));

CREATE TABLE organizer_profiles (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    display_name text NOT NULL,
    status       text NOT NULL DEFAULT 'active',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_organizer_profiles_display_name CHECK (length(trim(display_name)) > 0),
    CONSTRAINT chk_organizer_profiles_status CHECK (status IN ('active', 'suspended'))
);

CREATE INDEX idx_organizer_profiles_user_id ON organizer_profiles (user_id);

CREATE TABLE sessions (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash          text NOT NULL UNIQUE,
    idle_expires_at     timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    last_activity_at    timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_sessions_token_hash CHECK (length(token_hash) = 64),
    CONSTRAINT chk_sessions_expiry_order CHECK (absolute_expires_at >= idle_expires_at)
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_idle_expires_at ON sessions (idle_expires_at);

CREATE TABLE email_verification_tokens (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_evt_token_hash CHECK (length(token_hash) = 64)
);

CREATE INDEX idx_email_verification_tokens_user_id ON email_verification_tokens (user_id);

CREATE TABLE password_reset_tokens (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_prt_token_hash CHECK (length(token_hash) = 64)
);

CREATE INDEX idx_password_reset_tokens_user_id ON password_reset_tokens (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS email_verification_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS organizer_profiles;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd

-- +goose Up
-- +goose StatementBegin

-- 1. users: Enforce email normalization and verified-at requirement
UPDATE users SET email = lower(trim(email));

UPDATE users
SET email_verified_at = COALESCE(email_verified_at, created_at, now())
WHERE email_verified_at IS NULL;

ALTER TABLE users
    ALTER COLUMN email_verified_at SET DEFAULT now(),
    ALTER COLUMN email_verified_at SET NOT NULL,
    DROP COLUMN IF EXISTS email_verified;

DROP INDEX IF EXISTS idx_users_email_unique;

ALTER TABLE users
    ADD CONSTRAINT chk_users_email_normalized CHECK (email = lower(trim(email)) AND length(email) > 0),
    ADD CONSTRAINT uq_users_email UNIQUE (email);

-- 2. organizer_profiles: Remove redundant user_id index (covered by UNIQUE constraint)
DROP INDEX IF EXISTS idx_organizer_profiles_user_id;

-- 3. registration_verification_challenges: Replace obsolete email_verification_tokens
DROP TABLE IF EXISTS email_verification_tokens;

CREATE TABLE registration_verification_challenges (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    email       text NOT NULL,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_rvc_email_normalized CHECK (email = lower(trim(email)) AND length(email) > 0),
    CONSTRAINT chk_rvc_token_hash CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chk_rvc_consumption_order CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);

CREATE INDEX idx_rvc_email ON registration_verification_challenges (email);
CREATE INDEX idx_rvc_expires_at ON registration_verification_challenges (expires_at);

-- 4. password_reset_tokens: Strict token hash validation, consumption order, and expiration index
ALTER TABLE password_reset_tokens
    DROP CONSTRAINT IF EXISTS chk_prt_token_hash;

ALTER TABLE password_reset_tokens
    ADD CONSTRAINT chk_prt_token_hash CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT chk_prt_used_order CHECK (used_at IS NULL OR used_at >= created_at);

CREATE INDEX idx_password_reset_tokens_expires_at ON password_reset_tokens (expires_at);

-- 5. sessions: Strict token hash validation and absolute expiration index
ALTER TABLE sessions
    DROP CONSTRAINT IF EXISTS chk_sessions_token_hash;

ALTER TABLE sessions
    ADD CONSTRAINT chk_sessions_token_hash CHECK (token_hash ~ '^[0-9a-f]{64}$');

CREATE INDEX idx_sessions_absolute_expires_at ON sessions (absolute_expires_at);

-- 6. outbox_events: Lifecycle, retry scheduling, claims, and error tracking
ALTER TABLE outbox_events
    ADD COLUMN status text NOT NULL DEFAULT 'pending',
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN claim_token uuid,
    ADD COLUMN claimed_at timestamptz,
    ADD COLUMN claim_expires_at timestamptz,
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN processed_at timestamptz,
    ADD COLUMN last_error text,
    ADD COLUMN provider_message_id text;

UPDATE outbox_events
SET status = CASE WHEN published_at IS NOT NULL THEN 'published' ELSE 'pending' END,
    processed_at = published_at;

ALTER TABLE outbox_events
    ADD CONSTRAINT chk_outbox_events_status
        CHECK (status IN ('pending', 'processing', 'published', 'failed')),
    ADD CONSTRAINT chk_outbox_events_last_error
        CHECK (last_error IS NULL OR length(last_error) <= 2000),
    ADD CONSTRAINT chk_outbox_events_claim
        CHECK ((status = 'processing' AND claim_token IS NOT NULL AND claim_expires_at IS NOT NULL) OR (status != 'processing')),
    ADD CONSTRAINT chk_outbox_events_published
        CHECK ((status = 'published' AND published_at IS NOT NULL) OR (status != 'published'));

DROP INDEX IF EXISTS idx_outbox_events_unpublished;

CREATE INDEX idx_outbox_events_pending
    ON outbox_events (next_attempt_at, occurred_at)
    WHERE status = 'pending';

CREATE INDEX idx_outbox_events_expired_leases
    ON outbox_events (claim_expires_at)
    WHERE status = 'processing';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 6. Revert outbox_events
DROP INDEX IF EXISTS idx_outbox_events_expired_leases;
DROP INDEX IF EXISTS idx_outbox_events_pending;

CREATE INDEX idx_outbox_events_unpublished ON outbox_events (occurred_at) WHERE published_at IS NULL;

ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_published;
ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_claim;
ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_last_error;
ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_status;

ALTER TABLE outbox_events
    DROP COLUMN IF EXISTS provider_message_id,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS processed_at,
    DROP COLUMN IF EXISTS last_attempt_at,
    DROP COLUMN IF EXISTS claim_expires_at,
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS claim_token,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS status;

-- 5. Revert sessions
DROP INDEX IF EXISTS idx_sessions_absolute_expires_at;

ALTER TABLE sessions DROP CONSTRAINT IF EXISTS chk_sessions_token_hash;
ALTER TABLE sessions ADD CONSTRAINT chk_sessions_token_hash CHECK (length(token_hash) = 64);

-- 4. Revert password_reset_tokens
DROP INDEX IF EXISTS idx_password_reset_tokens_expires_at;

ALTER TABLE password_reset_tokens DROP CONSTRAINT IF EXISTS chk_prt_used_order;
ALTER TABLE password_reset_tokens DROP CONSTRAINT IF EXISTS chk_prt_token_hash;
ALTER TABLE password_reset_tokens ADD CONSTRAINT chk_prt_token_hash CHECK (length(token_hash) = 64);

-- 3. Revert registration_verification_challenges
DROP TABLE IF EXISTS registration_verification_challenges;

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

-- 2. Revert organizer_profiles
CREATE INDEX idx_organizer_profiles_user_id ON organizer_profiles (user_id);

-- 1. Revert users
ALTER TABLE users DROP CONSTRAINT IF EXISTS uq_users_email;
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_users_email_normalized;

CREATE UNIQUE INDEX idx_users_email_unique ON users (lower(trim(email)));

ALTER TABLE users
    ADD COLUMN email_verified boolean NOT NULL DEFAULT true;

ALTER TABLE users
    ALTER COLUMN email_verified_at DROP NOT NULL,
    ALTER COLUMN email_verified_at DROP DEFAULT;

-- +goose StatementEnd

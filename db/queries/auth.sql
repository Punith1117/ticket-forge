-- name: CreateRegistrationChallenge :one
INSERT INTO registration_verification_challenges (
    email,
    token_hash,
    expires_at
) VALUES (
    lower(trim(@email::text)),
    @token_hash,
    @expires_at
)
RETURNING *;

-- name: GetLatestRegistrationChallengeByEmail :one
SELECT * FROM registration_verification_challenges
WHERE email = lower(trim(@email::text))
ORDER BY created_at DESC
LIMIT 1;

-- name: GetRegistrationChallengeForUpdate :one
SELECT * FROM registration_verification_challenges
WHERE token_hash = @token_hash
FOR UPDATE;

-- name: ConsumeRegistrationChallenge :execrows
UPDATE registration_verification_challenges
SET consumed_at = now()
WHERE id = @id
  AND consumed_at IS NULL;

-- name: CreateSession :one
INSERT INTO sessions (
    user_id,
    token_hash,
    idle_expires_at,
    absolute_expires_at
) VALUES (
    @user_id,
    @token_hash,
    @idle_expires_at,
    @absolute_expires_at
)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions
WHERE token_hash = @token_hash
LIMIT 1;

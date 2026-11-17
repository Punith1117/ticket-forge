-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = lower(trim(@email::text)) LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
    email,
    password_hash,
    display_name
) VALUES (
    lower(trim(@email::text)), @password_hash, @display_name
)
RETURNING *;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = @password_hash,
    updated_at = now()
WHERE id = @id
RETURNING *;


-- name: CreateOrganizerProfile :one
INSERT INTO organizer_profiles (
    user_id,
    display_name
)
SELECT users.id, users.display_name
FROM users
WHERE users.id = $1
ON CONFLICT (user_id) DO NOTHING
RETURNING *;

-- name: GetOrganizerProfileByUserID :one
SELECT *
FROM organizer_profiles
WHERE user_id = $1
LIMIT 1;

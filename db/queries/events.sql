-- name: GetEventByID :one
SELECT * FROM events
WHERE id = $1 LIMIT 1;

-- name: ListEventsByVenue :many
SELECT * FROM events
WHERE venue_id = $1
ORDER BY starts_at ASC;

-- name: CreateEvent :one
INSERT INTO events (
    organizer_profile_id,
    venue_id,
    title,
    description,
    status,
    booking_status,
    starts_at,
    ends_at,
    sales_start_at,
    sales_end_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

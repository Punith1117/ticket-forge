-- name: GetEventByID :one
SELECT *
FROM events
WHERE id = $1
LIMIT 1;

-- name: ListAllEventsByVenue :many
SELECT e.*
FROM events e
JOIN venue_bookings vb
    ON vb.id = e.venue_booking_id
WHERE vb.venue_id = $1
ORDER BY vb.starts_at ASC;

-- name: ListPublicEventsByVenue :many
SELECT e.*
FROM events e
JOIN venue_bookings vb
    ON vb.id = e.venue_booking_id
WHERE vb.venue_id = $1
  AND e.status = 'active'
ORDER BY vb.starts_at ASC;

-- name: CreateEvent :one
INSERT INTO events (
    organizer_profile_id,
    venue_booking_id,
    title,
    description,
    status,
    booking_status,
    sales_start_at,
    sales_end_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

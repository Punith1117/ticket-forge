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

-- name: GetActiveEventByVenueBookingID :one
SELECT *
FROM events
WHERE venue_booking_id = $1
  AND status = 'active'
LIMIT 1;

-- name: CreateEvent :one
INSERT INTO events (
    organizer_profile_id,
    venue_booking_id,
    title,
    description,
    status,
    booking_status
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: CreateTicketCategory :one
INSERT INTO ticket_categories (
    event_id,
    name,
    price_minor,
    currency,
    capacity
) VALUES (
$1, $2, $3, 'INR', $4
)
RETURNING *;

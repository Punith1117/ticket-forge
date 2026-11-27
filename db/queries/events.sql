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

-- name: UpdateActiveEventBookingStatus :one
UPDATE events
SET
    booking_status = $2,
    updated_at = NOW()
WHERE id = $1
  AND status = 'active'
RETURNING *;

-- name: CancelEvent :one
UPDATE events
SET
    status = 'cancelled',
    booking_status = 'paused',
    updated_at = clock_timestamp()
WHERE id = @id
  AND organizer_profile_id = @organizer_profile_id
  AND status = 'active'
RETURNING *;

-- name: GetEventCancellationSummary :one
WITH event_scope AS (
    SELECT id, status, booking_status
    FROM events
    WHERE events.id = @id
      AND events.organizer_profile_id = @organizer_profile_id
),
reservation_stats AS (
    SELECT
        COALESCE(COUNT(r.id) FILTER (WHERE r.status = 'cancelled'), 0)::bigint AS cancelled_reservations
    FROM event_scope e
    JOIN ticket_categories tc ON tc.event_id = e.id
    JOIN reservations r ON r.ticket_category_id = tc.id
),
booking_stats AS (
    SELECT
        COALESCE(COUNT(b.id), 0)::bigint AS total_bookings,
        COALESCE(COUNT(b.id) FILTER (WHERE b.status = 'cancelled'), 0)::bigint AS cancelled_bookings
    FROM event_scope e
    JOIN ticket_categories tc ON tc.event_id = e.id
    JOIN reservations r ON r.ticket_category_id = tc.id
    JOIN bookings b ON b.reservation_id = r.id
),
refund_stats AS (
    SELECT
        COALESCE(COUNT(rf.id) FILTER (WHERE rf.status = 'pending'), 0)::bigint AS refunds_pending,
        COALESCE(COUNT(rf.id) FILTER (WHERE rf.status = 'processing'), 0)::bigint AS refunds_processing,
        COALESCE(COUNT(rf.id) FILTER (WHERE rf.status = 'succeeded'), 0)::bigint AS refunds_succeeded,
        COALESCE(COUNT(rf.id) FILTER (WHERE rf.status = 'failed'), 0)::bigint AS refunds_failed,
        COALESCE(SUM(rf.amount_minor) FILTER (WHERE rf.status = 'succeeded'), 0)::bigint AS total_refunded_amount_minor
    FROM event_scope e
    JOIN ticket_categories tc ON tc.event_id = e.id
    JOIN reservations r ON r.ticket_category_id = tc.id
    JOIN payment_attempts pa ON pa.reservation_id = r.id
    JOIN refunds rf ON rf.payment_attempt_id = pa.id
)
SELECT
    e.id AS event_id,
    e.status AS event_status,
    e.booking_status,
    COALESCE(bs.total_bookings, 0)::bigint AS total_bookings,
    COALESCE(bs.cancelled_bookings, 0)::bigint AS cancelled_bookings,
    COALESCE(rs.cancelled_reservations, 0)::bigint AS cancelled_reservations,
    COALESCE(rfs.refunds_pending, 0)::bigint AS refunds_pending,
    COALESCE(rfs.refunds_processing, 0)::bigint AS refunds_processing,
    COALESCE(rfs.refunds_succeeded, 0)::bigint AS refunds_succeeded,
    COALESCE(rfs.refunds_failed, 0)::bigint AS refunds_failed,
    COALESCE(rfs.total_refunded_amount_minor, 0)::bigint AS total_refunded_amount_minor
FROM event_scope e
LEFT JOIN reservation_stats rs ON true
LEFT JOIN booking_stats bs ON true
LEFT JOIN refund_stats rfs ON true;

-- name: GetReservationLifecycleReference :one
SELECT
    r.id AS reservation_id,
    r.ticket_category_id,
    tc.event_id
FROM reservations AS r
JOIN ticket_categories AS tc
    ON tc.id = r.ticket_category_id
WHERE r.id = $1;

-- ============================================================
-- BOOKINGS
-- ============================================================

-- name: CreateBooking :one
INSERT INTO bookings (
    id,
    reservation_id,
    payment_attempt_id,
    customer_id,
    quantity,
    unit_price_minor,
    total_price_minor,
    currency,
    status,
    confirmed_at
) VALUES (
    @id,
    @reservation_id,
    @payment_attempt_id,
    @customer_id,
    @quantity,
    @unit_price_minor,
    @total_price_minor,
    @currency,
    'confirmed',
    clock_timestamp()
)
RETURNING *;

-- name: GetBookingByReservationID :one
SELECT *
FROM bookings
WHERE reservation_id = @reservation_id
LIMIT 1;

-- name: GetBookingByID :one
SELECT *
FROM bookings
WHERE id = @id
LIMIT 1;

-- name: GetConfirmedBookingsForEventBatch :many
SELECT
    b.id,
    b.reservation_id,
    b.payment_attempt_id,
    b.customer_id,
    b.total_price_minor,
    b.currency
FROM bookings b
JOIN reservations r ON r.id = b.reservation_id
JOIN ticket_categories tc ON tc.id = r.ticket_category_id
WHERE tc.event_id = @event_id
  AND b.status = 'confirmed'
ORDER BY b.id ASC
LIMIT @limit_count
FOR UPDATE OF b SKIP LOCKED;

-- name: CancelBookingsBatch :execrows
UPDATE bookings
SET
    status = 'cancelled'
WHERE id = ANY(@booking_ids::uuid[]);

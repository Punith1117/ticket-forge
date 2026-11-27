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

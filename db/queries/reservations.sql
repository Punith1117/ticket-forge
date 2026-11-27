-- ============================================================
-- RESERVATION CREATION
-- ============================================================

-- name: LockBookableEvent :one
-- Locks the event and verifies that new reservations are allowed.
-- This must be the first lock acquired during reservation creation.
SELECT id, status, booking_status
FROM events
WHERE id = @event_id
  AND status = 'active'
  AND booking_status = 'open'
FOR UPDATE;

-- name: LockActiveTicketCategory :one
-- Locks the category and verifies that it belongs to the event.
SELECT id, event_id, name, price_minor, currency, capacity, is_active
FROM ticket_categories
WHERE id = @ticket_category_id
  AND event_id = @event_id
  AND is_active = true
FOR UPDATE;

-- name: LockInventory :one
-- Locks the inventory row and returns its current counters.
-- Capacity is already retrieved from the locked ticket category.
SELECT
    ticket_category_id,
    reserved_quantity,
    sold_quantity
FROM inventory
WHERE ticket_category_id = @ticket_category_id
FOR UPDATE;

-- name: IncrementReservedInventory :execrows
-- Atomically allocates inventory, refusing to exceed category capacity.
UPDATE inventory AS i
SET
    reserved_quantity = i.reserved_quantity + @quantity,
    updated_at = clock_timestamp()
FROM ticket_categories AS tc
WHERE i.ticket_category_id = @ticket_category_id
  AND tc.id = i.ticket_category_id
  AND @quantity > 0
  AND i.reserved_quantity + i.sold_quantity + @quantity <= tc.capacity;

-- name: CreateReservation :one
-- Price is snapshotted from the category inside the database.
-- The expiry is calculated using the database clock.
INSERT INTO reservations (
    customer_id,
    ticket_category_id,
    quantity,
    unit_price_minor,
    total_price_minor,
    currency,
    status,
    expires_at
)
SELECT
    sqlc.arg(customer_id),
    tc.id,
    sqlc.arg(quantity)::integer,
    tc.price_minor,
    sqlc.arg(quantity)::integer::bigint * tc.price_minor,
    tc.currency,
    'held',
    clock_timestamp() + sqlc.arg(hold_duration)::interval
FROM ticket_categories AS tc
WHERE tc.id = sqlc.arg(ticket_category_id)
    AND tc.event_id = sqlc.arg(event_id)
    AND tc.is_active = true
RETURNING *;

-- ============================================================
-- RESERVATION RETRIEVAL
-- ============================================================

-- name: GetReservationByID :one
SELECT *
FROM reservations
WHERE id = @id
LIMIT 1;

-- name: GetReservationByIDForCustomer :one
-- Customers can only retrieve their own reservations.
SELECT *
FROM reservations
WHERE id = @id
  AND customer_id = @customer_id
LIMIT 1;

-- name: GetReservationForUpdate :one
-- Must be called after locking the associated event.
SELECT *
FROM reservations
WHERE id = @id
FOR UPDATE;

-- name: GetReservationStatusForCustomer :one
-- Composite status query for the frontend polling endpoint.
SELECT
    r.id,
    r.customer_id,
    tc.event_id,
    r.ticket_category_id,
    r.quantity,
    r.unit_price_minor,
    r.total_price_minor,
    r.currency,
    CASE
        WHEN r.status = 'held' AND r.expires_at <= clock_timestamp() THEN 'expired'
        ELSE r.status
    END::text AS effective_reservation_status,
    r.cancellation_reason,
    r.expires_at,
    r.created_at,
    r.updated_at,

    b.id AS booking_id,
    b.status AS booking_status,
    b.confirmed_at AS booking_confirmed_at,

    COALESCE(bp.id, fallback_pa.id) AS payment_attempt_id,
    COALESCE(bp.status, fallback_pa.status, '')::text AS payment_status,
    COALESCE(bp.provider, fallback_pa.provider, '')::text AS payment_provider,
    COALESCE(bp.amount_minor, fallback_pa.amount_minor, 0)::bigint AS payment_amount_minor,
    COALESCE(bp.currency, fallback_pa.currency, '')::text AS payment_currency,
    COALESCE(bp.failure_reason, fallback_pa.failure_reason) AS payment_failure_reason,
    COALESCE(bp.attempted_at, fallback_pa.attempted_at) AS payment_attempted_at,
    COALESCE(bp.completed_at, fallback_pa.completed_at) AS payment_completed_at,

    rf.id AS refund_id,
    COALESCE(rf.status, '')::text AS refund_status,
    COALESCE(rf.amount_minor, 0)::bigint AS refund_amount_minor,
    COALESCE(rf.currency, '')::text AS refund_currency,
    rf.failure_reason AS refund_failure_reason,
    rf.created_at AS refund_created_at,
    rf.completed_at AS refund_completed_at
FROM reservations AS r
JOIN ticket_categories AS tc
    ON tc.id = r.ticket_category_id
LEFT JOIN bookings AS b
    ON b.reservation_id = r.id
LEFT JOIN payment_attempts AS bp
    ON bp.id = b.payment_attempt_id
LEFT JOIN LATERAL (
    SELECT
        p.id,
        p.status,
        p.provider,
        p.amount_minor,
        p.currency,
        p.failure_reason,
        p.attempted_at,
        p.completed_at
    FROM payment_attempts AS p
    WHERE b.payment_attempt_id IS NULL
      AND p.reservation_id = r.id
    ORDER BY
        CASE p.status
            WHEN 'succeeded' THEN 1
            WHEN 'pending' THEN 2
            WHEN 'initializing' THEN 3
            WHEN 'failed' THEN 4
            ELSE 5
        END,
        p.attempted_at DESC,
        p.id DESC
    LIMIT 1
) AS fallback_pa ON true
LEFT JOIN LATERAL (
    SELECT
        rfd.id,
        rfd.status,
        rfd.amount_minor,
        rfd.currency,
        rfd.failure_reason,
        rfd.created_at,
        rfd.completed_at
    FROM refunds AS rfd
    WHERE rfd.payment_attempt_id = COALESCE(bp.id, fallback_pa.id)
    ORDER BY rfd.created_at DESC, rfd.id DESC
    LIMIT 1
) AS rf ON true
WHERE r.id = @id
  AND r.customer_id = @customer_id
LIMIT 1;

-- ============================================================
-- RESERVATION EXPIRATION
-- ============================================================

-- name: GetExpiredReservationCandidates :many
-- This query only discovers candidates.
-- It intentionally does not lock reservations.
-- The worker locks the event first, then the reservation.
SELECT
    r.id,
    r.ticket_category_id,
    r.quantity,
    tc.event_id
FROM reservations AS r
JOIN ticket_categories AS tc
    ON tc.id = r.ticket_category_id
WHERE r.status = 'held'
  AND r.expires_at <= clock_timestamp()
ORDER BY r.expires_at ASC
LIMIT @limit_count;

-- name: LockEventForReservationLifecycle :one
-- Used by expiration and confirmation workflows.
-- Unlike LockBookableEvent, this does not require booking_status = open.
SELECT id, status, booking_status
FROM events
WHERE id = @event_id
FOR UPDATE;

-- name: ExpireReservation :execrows
-- Only expires a reservation that is still held and past its deadline.
UPDATE reservations
SET
    status = 'expired',
    updated_at = clock_timestamp()
WHERE id = @id
  AND status = 'held'
  AND expires_at <= clock_timestamp();

-- name: DecrementReservedInventory :execrows
-- Releases the inventory allocated to an expired reservation.
UPDATE inventory
SET
    reserved_quantity = reserved_quantity - @quantity,
    updated_at = clock_timestamp()
WHERE ticket_category_id = @ticket_category_id
  AND reserved_quantity >= @quantity;

-- name: ExpireExpiredReservationsForCategory :one
-- Claims and expires all currently expired holds for one ticket category.
-- Must be called after locking the associated event and ticket category.
-- Returns the total quantity that must be released from inventory.
WITH expired AS MATERIALIZED (
    SELECT
        id,
        quantity
    FROM reservations
    WHERE reservations.ticket_category_id = @ticket_category_id
      AND status = 'held'
      AND expires_at <= clock_timestamp()
    ORDER BY expires_at ASC
    FOR UPDATE
),
updated AS (
    UPDATE reservations AS r
    SET
        status = 'expired',
        updated_at = clock_timestamp()
    FROM expired AS e
    WHERE r.id = e.id
      AND r.status = 'held'
      AND r.expires_at <= clock_timestamp()
    RETURNING r.quantity
)
SELECT COALESCE(SUM(quantity), 0)::integer AS released_quantity
FROM updated;

-- ============================================================
-- RESERVATION CONFIRMATION & INVENTORY CONVERSION
-- ============================================================

-- name: ConfirmReservation :execrows
UPDATE reservations
SET
    status = 'confirmed',
    updated_at = clock_timestamp()
WHERE id = @id
  AND status = 'held'
  AND expires_at > clock_timestamp();

-- name: ConvertReservedToSoldInventory :execrows
UPDATE inventory
SET
    reserved_quantity = reserved_quantity - @quantity,
    sold_quantity = sold_quantity + @quantity,
    updated_at = clock_timestamp()
WHERE ticket_category_id = @ticket_category_id
  AND reserved_quantity >= @quantity;


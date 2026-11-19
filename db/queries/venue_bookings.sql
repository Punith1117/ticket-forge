-- name: LockOrganizerProfile :one
SELECT id
FROM organizer_profiles
WHERE id = $1
FOR UPDATE;

-- name: GetActiveVenueForBooking :one
SELECT
    id,
    capacity,
    timezone
FROM venues
WHERE id = $1
  AND is_active = true
FOR SHARE;

-- name: CountActiveUnfinishedVenueBookings :one
SELECT COUNT(*)::int
FROM venue_bookings
WHERE organizer_profile_id = $1
  AND status = 'active'
  AND ends_at > now();

-- name: CreateVenueBooking :one
INSERT INTO venue_bookings (
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at
)
VALUES ($1, $2, $3, $4)
RETURNING
    id,
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at,
    status,
    created_at,
    updated_at;

-- name: GetVenueBookingByID :one
SELECT
    id,
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at,
    status,
    created_at,
    updated_at
FROM venue_bookings
WHERE id = $1;

-- name: GetVenueBookingForOrganizer :one
SELECT
    id,
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at,
    status,
    created_at,
    updated_at
FROM venue_bookings
WHERE id = $1
  AND organizer_profile_id = $2;

-- name: ListOrganizerVenueBookings :many
SELECT
    id,
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at,
    status,
    created_at,
    updated_at
FROM venue_bookings
WHERE organizer_profile_id = $1
ORDER BY created_at DESC, id DESC;

-- name: LockVenueBookingForOrganizer :one
SELECT
    id,
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at,
    status
FROM venue_bookings
WHERE id = $1
  AND organizer_profile_id = $2
FOR UPDATE;

-- name: HasActiveEventForVenueBooking :one
SELECT EXISTS (
    SELECT 1
    FROM events
    WHERE venue_booking_id = $1
      AND status = 'active'
);

-- name: CancelVenueBooking :one
UPDATE venue_bookings
SET
    status = 'cancelled',
    updated_at = now()
WHERE id = $1
  AND organizer_profile_id = $2
  AND status = 'active'
RETURNING
    id,
    organizer_profile_id,
    venue_id,
    starts_at,
    ends_at,
    status,
    created_at,
    updated_at;

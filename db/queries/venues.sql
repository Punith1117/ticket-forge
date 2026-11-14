-- name: GetVenueByID :one
SELECT * FROM venues
WHERE id = $1 LIMIT 1;

-- name: GetVenueBySlug :one
SELECT * FROM venues
WHERE slug = $1 LIMIT 1;

-- name: ListActiveVenues :many
SELECT * FROM venues
WHERE is_active = true
ORDER BY city ASC, name ASC;

-- name: CreateVenue :one
INSERT INTO venues (
    name,
    slug,
    street_address,
    city,
    state,
    postal_code,
    country_code,
    timezone,
    capacity,
    is_active
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

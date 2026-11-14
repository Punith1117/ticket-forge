-- +goose Up
-- +goose StatementBegin
CREATE TABLE venues (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    name           text NOT NULL,
    slug           text NOT NULL UNIQUE,
    street_address text NOT NULL,
    city           text NOT NULL,
    state          text NOT NULL,
    postal_code    text NOT NULL,
    country_code   char(2) NOT NULL DEFAULT 'IN',
    timezone       text NOT NULL DEFAULT 'Asia/Kolkata',
    capacity       integer NOT NULL,
    is_active      boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_venues_name_non_empty CHECK (length(trim(name)) > 0),
    CONSTRAINT chk_venues_slug_non_empty CHECK (length(trim(slug)) > 0),
    CONSTRAINT chk_venues_postal_code CHECK (postal_code ~ '^[0-9]{6}$'),
    CONSTRAINT chk_venues_country_code CHECK (country_code = 'IN'),
    CONSTRAINT chk_venues_capacity_positive CHECK (capacity > 0),
    CONSTRAINT chk_venues_timezone_valid CHECK (now() AT TIME ZONE timezone IS NOT NULL)
);

CREATE UNIQUE INDEX idx_venues_name_city_unique ON venues (lower(trim(name)), lower(trim(city)));
CREATE INDEX idx_venues_city_active ON venues (city) WHERE is_active = true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS venues;
-- +goose StatementEnd

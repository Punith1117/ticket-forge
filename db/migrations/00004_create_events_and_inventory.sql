-- +goose Up
-- +goose StatementBegin
CREATE TABLE events (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    organizer_profile_id uuid NOT NULL REFERENCES organizer_profiles(id) ON DELETE RESTRICT,
    venue_id             uuid NOT NULL REFERENCES venues(id) ON DELETE RESTRICT,
    title                text NOT NULL,
    description          text NOT NULL DEFAULT '',
    status               text NOT NULL DEFAULT 'active',
    booking_status       text NOT NULL DEFAULT 'open',
    starts_at            timestamptz NOT NULL,
    ends_at              timestamptz NOT NULL,
    sales_start_at       timestamptz NOT NULL,
    sales_end_at         timestamptz NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    schedule_range       tstzrange GENERATED ALWAYS AS (
        tstzrange(starts_at, ends_at, '[)')
    ) STORED,

    CONSTRAINT chk_events_title_non_empty CHECK (length(trim(title)) > 0),
    CONSTRAINT chk_events_status CHECK (status IN ('active', 'cancelled')),
    CONSTRAINT chk_events_booking_status CHECK (booking_status IN ('open', 'paused')),
    CONSTRAINT chk_events_duration CHECK (ends_at > starts_at),
    CONSTRAINT chk_events_sales_window CHECK (
        sales_end_at > sales_start_at AND sales_end_at <= starts_at
    ),

    CONSTRAINT events_venue_no_overlap EXCLUDE USING gist (
        venue_id WITH =,
        schedule_range WITH &&
    ) WHERE (status = 'active')
);

CREATE INDEX idx_events_organizer_profile_id ON events (organizer_profile_id);
CREATE INDEX idx_events_venue_id ON events (venue_id);

CREATE TABLE ticket_categories (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    event_id    uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name        text NOT NULL,
    price_minor bigint NOT NULL,
    currency    char(3) NOT NULL DEFAULT 'INR',
    capacity    integer NOT NULL,
    is_active   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_ticket_categories_name_non_empty CHECK (length(trim(name)) > 0),
    CONSTRAINT chk_ticket_categories_price_minor CHECK (price_minor >= 0),
    CONSTRAINT chk_ticket_categories_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_ticket_categories_capacity CHECK (capacity > 0)
);

CREATE INDEX idx_ticket_categories_event_id ON ticket_categories (event_id);

CREATE TABLE inventory (
    ticket_category_id uuid PRIMARY KEY REFERENCES ticket_categories(id) ON DELETE CASCADE,
    reserved_quantity  integer NOT NULL DEFAULT 0,
    sold_quantity      integer NOT NULL DEFAULT 0,
    updated_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_inventory_reserved_non_negative CHECK (reserved_quantity >= 0),
    CONSTRAINT chk_inventory_sold_non_negative CHECK (sold_quantity >= 0)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inventory;
DROP TABLE IF EXISTS ticket_categories;
DROP TABLE IF EXISTS events;
-- +goose StatementEnd

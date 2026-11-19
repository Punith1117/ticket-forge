-- +goose Up

-- Create venue bookings. A venue booking owns the venue and schedule.
CREATE TABLE venue_bookings (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    organizer_profile_id uuid NOT NULL
        REFERENCES organizer_profiles(id) ON DELETE RESTRICT,
    venue_id uuid NOT NULL
        REFERENCES venues(id) ON DELETE RESTRICT,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    schedule_range tstzrange GENERATED ALWAYS AS (
        tstzrange(starts_at, ends_at, '[)')
    ) STORED,

    CONSTRAINT uq_venue_bookings_id_organizer
        UNIQUE (id, organizer_profile_id),

    CONSTRAINT chk_venue_bookings_status
        CHECK (status IN ('active', 'cancelled')),

    CONSTRAINT chk_venue_bookings_duration
        CHECK (
            ends_at - starts_at >= interval '15 minutes'
            AND ends_at - starts_at <= interval '7 days'
        ),

    CONSTRAINT venue_bookings_venue_no_overlap
        EXCLUDE USING gist (
            venue_id WITH =,
            schedule_range WITH &&
        )
        WHERE (status = 'active')
);

CREATE INDEX idx_venue_bookings_organizer_profile_id
    ON venue_bookings (organizer_profile_id);

CREATE INDEX idx_venue_bookings_venue_id
    ON venue_bookings (venue_id);


-- Remove event-owned venue and scheduling constraints.
ALTER TABLE events
    DROP CONSTRAINT events_venue_no_overlap,
    DROP CONSTRAINT chk_events_duration,
    DROP CONSTRAINT chk_events_sales_window,
    DROP CONSTRAINT events_venue_id_fkey;

DROP INDEX idx_events_venue_id;

-- Remove event-owned scheduling and venue columns.
ALTER TABLE events
    DROP COLUMN schedule_range,
    DROP COLUMN venue_id,
    DROP COLUMN starts_at,
    DROP COLUMN ends_at;

-- Associate each event with a venue booking instead.
ALTER TABLE events
    ADD COLUMN venue_booking_id uuid NOT NULL;

-- Ensure the event and its venue booking belong to the same organizer.
ALTER TABLE events
    ADD CONSTRAINT fk_events_venue_booking_organizer
    FOREIGN KEY (venue_booking_id, organizer_profile_id)
    REFERENCES venue_bookings (id, organizer_profile_id)
    ON DELETE RESTRICT;

-- At most one active event can reference a venue booking.
CREATE UNIQUE INDEX uq_events_active_venue_booking
    ON events (venue_booking_id)
    WHERE status = 'active';

-- The event's sales window can no longer reference its own start time.
-- Comparing sales_end_at with venue_bookings.starts_at is application validation.
ALTER TABLE events
    ADD CONSTRAINT chk_events_sales_window
    CHECK (sales_end_at > sales_start_at);


-- +goose Down

-- Remove constraints and indexes introduced for the new relationship.
DROP INDEX uq_events_active_venue_booking;

ALTER TABLE events
    DROP CONSTRAINT fk_events_venue_booking_organizer,
    DROP CONSTRAINT chk_events_sales_window;

-- Restore the original event-owned venue and scheduling columns.
ALTER TABLE events
    ADD COLUMN venue_id uuid,
    ADD COLUMN starts_at timestamptz,
    ADD COLUMN ends_at timestamptz;

-- Recover venue and schedule information from associated venue bookings.
UPDATE events e
SET
    venue_id = vb.venue_id,
    starts_at = vb.starts_at,
    ends_at = vb.ends_at
FROM venue_bookings vb
WHERE e.venue_booking_id = vb.id;

ALTER TABLE events
    ALTER COLUMN venue_id SET NOT NULL,
    ALTER COLUMN starts_at SET NOT NULL,
    ALTER COLUMN ends_at SET NOT NULL;

-- Restore the original scheduling range.
ALTER TABLE events
    ADD COLUMN schedule_range tstzrange
    GENERATED ALWAYS AS (
        tstzrange(starts_at, ends_at, '[)')
    ) STORED;

-- Restore the original event constraints.
ALTER TABLE events
    ADD CONSTRAINT events_venue_id_fkey
        FOREIGN KEY (venue_id)
        REFERENCES venues(id)
        ON DELETE RESTRICT,

    ADD CONSTRAINT chk_events_duration
        CHECK (ends_at > starts_at),

    ADD CONSTRAINT chk_events_sales_window
        CHECK (
            sales_end_at > sales_start_at
            AND sales_end_at <= starts_at
        );

CREATE INDEX idx_events_venue_id
    ON events (venue_id);

ALTER TABLE events
    DROP COLUMN venue_booking_id;

-- Restore event-level venue scheduling protection.
ALTER TABLE events
    ADD CONSTRAINT events_venue_no_overlap
    EXCLUDE USING gist (
        venue_id WITH =,
        schedule_range WITH &&
    )
    WHERE (status = 'active');

-- Remove the venue booking model.
DROP TABLE venue_bookings;

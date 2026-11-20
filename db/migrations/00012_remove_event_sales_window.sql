-- +goose Up

ALTER TABLE events
    DROP CONSTRAINT IF EXISTS chk_events_sales_window;

ALTER TABLE events
    DROP COLUMN sales_start_at,
    DROP COLUMN sales_end_at;


-- +goose Down

ALTER TABLE events
    ADD COLUMN sales_start_at timestamptz,
    ADD COLUMN sales_end_at timestamptz;

-- Restore the previous sales window using the associated venue booking.
UPDATE events e
SET
    sales_start_at = vb.starts_at,
    sales_end_at = vb.ends_at
FROM venue_bookings vb
WHERE vb.id = e.venue_booking_id;

ALTER TABLE events
    ALTER COLUMN sales_start_at SET NOT NULL,
    ALTER COLUMN sales_end_at SET NOT NULL;

ALTER TABLE events
    ADD CONSTRAINT chk_events_sales_window
    CHECK (sales_end_at > sales_start_at);

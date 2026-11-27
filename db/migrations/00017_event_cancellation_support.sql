-- +goose Up
-- +goose StatementBegin

-- Fast discovery of active bookings for batch cancellation by ticket category / event
CREATE INDEX IF NOT EXISTS idx_bookings_confirmed_cancellation
    ON bookings (id)
    WHERE status = 'confirmed';

-- Fast discovery of held reservations for batch cancellation
CREATE INDEX IF NOT EXISTS idx_reservations_held_category
    ON reservations (ticket_category_id)
    WHERE status = 'held';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_reservations_held_category;
DROP INDEX IF EXISTS idx_bookings_confirmed_cancellation;
-- +goose StatementEnd

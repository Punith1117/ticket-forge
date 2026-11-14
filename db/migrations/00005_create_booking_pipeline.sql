-- +goose Up
-- +goose StatementBegin
CREATE TABLE reservations (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    customer_id          uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    ticket_category_id   uuid NOT NULL REFERENCES ticket_categories(id) ON DELETE RESTRICT,
    quantity             integer NOT NULL,
    unit_price_minor     bigint NOT NULL,
    total_price_minor    bigint NOT NULL,
    currency             char(3) NOT NULL DEFAULT 'INR',
    status               text NOT NULL DEFAULT 'held',
    cancellation_reason  text,
    expires_at           timestamptz NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_reservations_quantity CHECK (quantity > 0),
    CONSTRAINT chk_reservations_unit_price CHECK (unit_price_minor >= 0),
    CONSTRAINT chk_reservations_total_price CHECK (total_price_minor = quantity * unit_price_minor),
    CONSTRAINT chk_reservations_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_reservations_status CHECK (status IN ('held', 'confirmed', 'expired', 'cancelled'))
);

CREATE INDEX idx_reservations_customer_id ON reservations (customer_id);
CREATE INDEX idx_reservations_ticket_category_id ON reservations (ticket_category_id);
CREATE INDEX idx_reservations_active_expiry ON reservations (expires_at) WHERE status = 'held';

CREATE TABLE payment_attempts (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    reservation_id     uuid NOT NULL REFERENCES reservations(id) ON DELETE RESTRICT,
    amount_minor       bigint NOT NULL,
    currency           char(3) NOT NULL DEFAULT 'INR',
    status             text NOT NULL DEFAULT 'pending',
    provider_reference text,
    failure_reason     text,
    attempted_at       timestamptz NOT NULL DEFAULT now(),
    completed_at       timestamptz,

    CONSTRAINT chk_payment_attempts_amount CHECK (amount_minor > 0),
    CONSTRAINT chk_payment_attempts_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_payment_attempts_status CHECK (status IN ('pending', 'succeeded', 'failed'))
);

CREATE INDEX idx_payment_attempts_reservation_id ON payment_attempts (reservation_id);

CREATE TABLE bookings (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    reservation_id    uuid NOT NULL UNIQUE REFERENCES reservations(id) ON DELETE RESTRICT,
    customer_id       uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quantity          integer NOT NULL,
    unit_price_minor  bigint NOT NULL,
    total_price_minor bigint NOT NULL,
    currency          char(3) NOT NULL DEFAULT 'INR',
    status            text NOT NULL DEFAULT 'confirmed',
    confirmed_at      timestamptz NOT NULL DEFAULT now(),
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_bookings_quantity CHECK (quantity > 0),
    CONSTRAINT chk_bookings_unit_price CHECK (unit_price_minor >= 0),
    CONSTRAINT chk_bookings_total_price CHECK (total_price_minor = quantity * unit_price_minor),
    CONSTRAINT chk_bookings_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_bookings_status CHECK (status IN ('confirmed', 'cancelled'))
);

CREATE INDEX idx_bookings_customer_id ON bookings (customer_id);
CREATE INDEX idx_bookings_reservation_id ON bookings (reservation_id);

CREATE TABLE refunds (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    booking_id     uuid NOT NULL UNIQUE REFERENCES bookings(id) ON DELETE RESTRICT,
    amount_minor   bigint NOT NULL,
    currency       char(3) NOT NULL DEFAULT 'INR',
    status         text NOT NULL DEFAULT 'pending',
    failure_reason text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    completed_at   timestamptz,

    CONSTRAINT chk_refunds_amount CHECK (amount_minor > 0),
    CONSTRAINT chk_refunds_currency CHECK (currency = 'INR'),
    CONSTRAINT chk_refunds_status CHECK (status IN ('pending', 'succeeded', 'failed'))
);

CREATE INDEX idx_refunds_booking_id ON refunds (booking_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS payment_attempts;
DROP TABLE IF EXISTS reservations;
-- +goose StatementEnd

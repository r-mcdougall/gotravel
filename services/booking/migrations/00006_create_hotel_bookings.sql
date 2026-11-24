-- +goose Up
CREATE TABLE hotel_bookings (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL,
    room_type_id    UUID        NOT NULL REFERENCES room_types (room_type_id),
    status          TEXT        NOT NULL CHECK (status IN ('HELD', 'CONFIRMED', 'EXPIRED', 'CANCELLED')),
    check_in        DATE        NOT NULL,
    check_out       DATE        NOT NULL,
    guests          INTEGER     NOT NULL CHECK (guests > 0),
    guest_name      TEXT        NOT NULL,
    price_per_night BIGINT      NOT NULL CHECK (price_per_night >= 0),
    expires_at      TIMESTAMPTZ NOT NULL,
    payment_id      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT hotel_bookings_stay_order CHECK (check_out > check_in),
    CONSTRAINT hotel_bookings_paid_when_confirmed CHECK ((status = 'CONFIRMED') = (payment_id IS NOT NULL))
);

CREATE INDEX hotel_bookings_user_created_idx ON hotel_bookings (user_id, created_at DESC, id DESC);
CREATE INDEX hotel_bookings_held_expiry_idx ON hotel_bookings (expires_at) WHERE status = 'HELD';

-- +goose Down
DROP TABLE hotel_bookings;
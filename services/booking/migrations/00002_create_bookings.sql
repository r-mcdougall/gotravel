-- +goose Up
CREATE TABLE bookings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    flight_id       UUID NOT NULL REFERENCES flight_inventories (flight_id),
    status          TEXT NOT NULL CHECK (status IN ('HELD', 'CONFIRMED', 'EXPIRED', 'CANCELLED')),
    passenger_count INTEGER     NOT NULL CHECK (passenger_count > 0),
    seat_price      BIGINT      NOT NULL CHECK (seat_price >= 0),
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX bookings_user_created_idx ON bookings (user_id, created_at DESC, id DESC);
CREATE INDEX bookings_held_expiry_idx ON bookings (expires_at) WHERE status = 'HELD';

CREATE TABLE passengers (
    id         UUID     PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID     NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    seq        SMALLINT NOT NULL,
    full_name  TEXT     NOT NULL,
    birth_date DATE     NOT NULL,
    UNIQUE (booking_id, seq)
);

-- +goose Down
DROP TABLE passengers;
DROP TABLE bookings;

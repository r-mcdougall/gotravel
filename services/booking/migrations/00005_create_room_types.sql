-- +goose Up
CREATE TABLE room_types (
    room_type_id    UUID        PRIMARY KEY,
    hotel_id        UUID        NOT NULL,
    hotel_name      TEXT        NOT NULL,
    city_code       TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    max_guests      INTEGER     NOT NULL CHECK (max_guests > 0),
    total_rooms     INTEGER     NOT NULL CHECK (total_rooms > 0),
    price_per_night BIGINT      NOT NULL CHECK (price_per_night > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE room_nights (
    room_type_id UUID        NOT NULL REFERENCES room_types (room_type_id),
    night        DATE        NOT NULL,
    total_rooms  INTEGER     NOT NULL,
    held_rooms   INTEGER     NOT NULL DEFAULT 0 CHECK (held_rooms >= 0),
    sold_rooms   INTEGER     NOT NULL DEFAULT 0 CHECK (sold_rooms >= 0),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (room_type_id, night),
    CONSTRAINT room_nights_within_total CHECK (held_rooms + sold_rooms <= total_rooms)
);

-- +goose Down
DROP TABLE room_nights;
DROP TABLE room_types;
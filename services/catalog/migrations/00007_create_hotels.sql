-- +goose Up
CREATE TABLE cities (
    code TEXT PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
    name TEXT NOT NULL,

    CONSTRAINT cities_name_key UNIQUE (name)
);

INSERT INTO cities (code, name) VALUES
    ('JKT', 'Jakarta'),
    ('SUB', 'Surabaya'),
    ('MES', 'Medan'),
    ('JOG', 'Yogyakarta'),
    ('DPS', 'Denpasar'),
    ('UPG', 'Makassar'),
    ('BPN', 'Balikpapan'),
    ('DJJ', 'Jayapura'),
    ('SIN', 'Singapore'),
    ('KUL', 'Kuala Lumpur'),
    ('TYO', 'Tokyo');

CREATE TABLE hotels (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    city_code   TEXT        NOT NULL,
    address     TEXT        NOT NULL,
    star_rating INTEGER     NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT hotels_city_fkey FOREIGN KEY (city_code) REFERENCES cities (code),
    CONSTRAINT hotels_name_key UNIQUE (city_code, name),
    CONSTRAINT hotels_star_rating_range CHECK (star_rating BETWEEN 1 AND 5)
);

CREATE TABLE room_types (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    hotel_id        UUID        NOT NULL,
    name            TEXT        NOT NULL,
    max_guests      INTEGER     NOT NULL,
    total_rooms     INTEGER     NOT NULL CHECK (total_rooms > 0),
    price_per_night BIGINT      NOT NULL CHECK (price_per_night > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT room_types_hotel_fkey FOREIGN KEY (hotel_id) REFERENCES hotels (id),
    CONSTRAINT room_types_name_key UNIQUE (hotel_id, name),
    CONSTRAINT room_types_max_guests_range CHECK (max_guests BETWEEN 1 AND 10)
);

-- +goose Down
DROP TABLE room_types;
DROP TABLE hotels;
DROP TABLE cities;

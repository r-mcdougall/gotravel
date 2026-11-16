-- +goose Up
CREATE TABLE flight_inventories (
    flight_id            UUID        PRIMARY KEY,
    version              INTEGER     NOT NULL,
    flight_number        TEXT        NOT NULL,
    airline_code         TEXT        NOT NULL,
    origin_code          TEXT        NOT NULL,
    destination_code     TEXT        NOT NULL,
    departure_at         TIMESTAMPTZ NOT NULL,
    arrival_at           TIMESTAMPTZ NOT NULL,
    price                BIGINT      NOT NULL,
    capacity             INTEGER     NOT NULL,
    held_seats           INTEGER     NOT NULL DEFAULT 0 CHECK (held_seats >= 0),
    sold_seats           INTEGER     NOT NULL DEFAULT 0 CHECK (sold_seats >= 0),
    availability_version BIGINT      NOT NULL DEFAULT 0,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE flight_inventories;

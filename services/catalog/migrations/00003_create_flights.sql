-- +goose Up
CREATE TABLE flights (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    flight_number    TEXT        NOT NULL CHECK (flight_number ~ '^[A-Z0-9]{2}[0-9]{1,4}$'),
    airline_code     TEXT        NOT NULL,
    origin_code      TEXT        NOT NULL,
    destination_code TEXT        NOT NULL,
    departure_at     TIMESTAMPTZ NOT NULL,
    arrival_at       TIMESTAMPTZ NOT NULL,
    price            BIGINT      NOT NULL CHECK (price > 0),
    capacity         INTEGER     NOT NULL CHECK (capacity > 0),
    available_seats  INTEGER     NOT NULL,
    version          INTEGER     NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT flights_airline_fkey FOREIGN KEY (airline_code) REFERENCES airlines (code),
    CONSTRAINT flights_origin_fkey FOREIGN KEY (origin_code) REFERENCES airports (code),
    CONSTRAINT flights_destination_fkey FOREIGN KEY (destination_code) REFERENCES airports (code),
    CONSTRAINT flights_departure_key UNIQUE (airline_code, flight_number, departure_at),
    CONSTRAINT flights_number_matches_airline CHECK (left(flight_number, 2) = airline_code),
    CONSTRAINT flights_distinct_airports CHECK (origin_code <> destination_code),
    CONSTRAINT flights_arrival_after_departure CHECK (arrival_at > departure_at),
    CONSTRAINT flights_available_seats_range CHECK (available_seats BETWEEN 0 AND capacity)
);

CREATE INDEX flights_route_departure_idx ON flights (origin_code, destination_code, departure_at);

-- +goose Down
DROP TABLE flights;

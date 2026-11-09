-- +goose Up
CREATE TABLE airports (
    code        TEXT PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
    name        TEXT NOT NULL,
    city        TEXT NOT NULL,
    timezone    TEXT NOT NULL
);

-- +goose Down
DROP TABLE airports;

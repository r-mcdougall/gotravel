-- +goose Up
CREATE TABLE airlines (
    code TEXT PRIMARY KEY CHECK (code ~ '^[A-Z0-9]{2}$'),
    name TEXT NOT NULL
);

-- +goose Down
DROP TABLE airlines;

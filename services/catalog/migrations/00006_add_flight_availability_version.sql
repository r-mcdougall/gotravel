-- +goose Up
ALTER TABLE flights ADD COLUMN availability_version BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE flights DROP COLUMN availability_version;

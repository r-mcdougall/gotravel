-- +goose Up
CREATE TABLE payments (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    amount     BIGINT      NOT NULL CHECK (amount >= 0),
    status     TEXT        NOT NULL CHECK (status IN ('SUCCEEDED', 'FAILED', 'REVERSED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A booking is charged at most once; failed and reversed payments do not count.
CREATE UNIQUE INDEX payments_one_success_per_booking ON payments (booking_id) WHERE status = 'SUCCEEDED';

-- +goose Down
DROP TABLE payments;

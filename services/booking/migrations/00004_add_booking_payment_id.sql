-- +goose Up
ALTER TABLE bookings
    ADD COLUMN payment_id UUID,
    ADD CONSTRAINT bookings_paid_when_confirmed CHECK ((status = 'CONFIRMED') = (payment_id IS NOT NULL));

-- +goose Down
ALTER TABLE bookings
    DROP CONSTRAINT bookings_paid_when_confirmed,
    DROP COLUMN payment_id;
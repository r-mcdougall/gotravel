-- name: HoldSeats :one
UPDATE flight_inventories
SET held_seats           = held_seats + @seats,
    availability_version = availability_version + 1,
    updated_at           = now()
WHERE flight_id = @flight_id
  AND departure_at > now()
  AND capacity - held_seats - sold_seats >= @seats
RETURNING *;

-- name: ReleaseHeldSeats :one
UPDATE flight_inventories
SET held_seats           = held_seats - @seats,
    availability_version = availability_version + 1,
    updated_at           = now()
WHERE flight_id = @flight_id
RETURNING *;

-- name: CreateBooking :one
INSERT INTO bookings (user_id, flight_id, status, passenger_count, seat_price, expires_at)
VALUES (@user_id, @flight_id, 'HELD', @passenger_count, @seat_price, @expires_at)
RETURNING *;

-- name: CreatePassengers :copyfrom
INSERT INTO passengers (booking_id, seq, full_name, birth_date)
VALUES (@booking_id, @seq, @full_name, @birth_date);

-- name: GetBooking :one
SELECT sqlc.embed(bookings), sqlc.embed(flight_inventories)
FROM bookings
         JOIN flight_inventories ON flight_inventories.flight_id = bookings.flight_id
WHERE bookings.id = @id AND bookings.user_id = @user_id;

-- name: ListBookings :many
SELECT sqlc.embed(bookings), sqlc.embed(flight_inventories)
FROM bookings
         JOIN flight_inventories ON flight_inventories.flight_id = bookings.flight_id
WHERE bookings.user_id = @user_id
ORDER BY bookings.created_at DESC, bookings.id DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountBookings :one
SELECT count(*) FROM bookings WHERE user_id = @user_id;

-- name: ListPassengers :many
SELECT * FROM passengers
WHERE booking_id = ANY(@booking_ids::uuid[])
ORDER BY booking_id, seq;

-- name: CancelBooking :one
UPDATE bookings
SET status = 'CANCELLED', updated_at = now()
WHERE id = @id AND user_id = @user_id AND status = 'HELD'
RETURNING *;

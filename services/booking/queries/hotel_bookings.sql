-- name: CreateHotelBooking :one
INSERT INTO hotel_bookings (user_id, room_type_id, status, check_in, check_out, guests, guest_name, price_per_night, expires_at)
VALUES (@user_id, @room_type_id, 'HELD', @check_in, @check_out, @guests, @guest_name, @price_per_night, @expires_at)
RETURNING *;

-- name: GetHotelBooking :one
SELECT sqlc.embed(hotel_bookings), sqlc.embed(room_types)
FROM hotel_bookings
JOIN room_types ON room_types.room_type_id = hotel_bookings.room_type_id
WHERE hotel_bookings.id = @id AND hotel_bookings.user_id = @user_id;

-- name: ListHotelBookings :many
SELECT sqlc.embed(hotel_bookings), sqlc.embed(room_types)
FROM hotel_bookings
JOIN room_types ON room_types.room_type_id = hotel_bookings.room_type_id
WHERE hotel_bookings.user_id = @user_id
ORDER BY hotel_bookings.created_at DESC, hotel_bookings.id DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountHotelBookings :one
SELECT count(*) FROM hotel_bookings WHERE user_id = @user_id;

-- name: CancelHotelBooking :one
UPDATE hotel_bookings
SET status = 'CANCELLED', updated_at = now()
WHERE id = @id AND user_id = @user_id AND status = 'HELD'
RETURNING *;

-- name: ExpireHotelHold :one
UPDATE hotel_bookings
SET status = 'EXPIRED', updated_at = now()
WHERE id = (
    SELECT id FROM hotel_bookings
    WHERE status = 'HELD' AND expires_at <= now()
    ORDER BY expires_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, room_type_id, check_in, check_out;

-- name: ConfirmHotelBooking :one
UPDATE hotel_bookings
SET status = 'CONFIRMED', payment_id = @payment_id::uuid, updated_at = now()
WHERE id = @id AND status = 'HELD'
RETURNING id, room_type_id, check_in, check_out;

-- name: GetHotelBookingPayment :one
SELECT status, payment_id FROM hotel_bookings WHERE id = @id;

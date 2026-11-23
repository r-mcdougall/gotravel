-- name: InsertRoomType :exec
INSERT INTO room_types (room_type_id, hotel_id, hotel_name, city_code, name, max_guests, total_rooms, price_per_night)
VALUES (@room_type_id, @hotel_id, @hotel_name, @city_code, @name, @max_guests, @total_rooms, @price_per_night)
ON CONFLICT (room_type_id) DO NOTHING;

-- name: GetRoomType :one
SELECT * FROM room_types WHERE room_type_id = @room_type_id;

-- name: EnsureRoomNights :exec
INSERT INTO room_nights (room_type_id, night, total_rooms)
SELECT rt.room_type_id, night::date, rt.total_rooms
FROM room_types rt
CROSS JOIN generate_series(@check_in::date, (@check_out::date) - 1, interval '1 day') AS night
WHERE rt.room_type_id = @room_type_id
ORDER BY night
ON CONFLICT (room_type_id, night) DO NOTHING;

-- name: LockRoomNights :many
SELECT * FROM room_nights
WHERE room_type_id = @room_type_id AND night >= @check_in AND night < @check_out
ORDER BY night
FOR UPDATE;

-- name: HoldRoomNights :exec
UPDATE room_nights
SET held_rooms = held_rooms + 1, updated_at = now()
WHERE room_type_id = @room_type_id AND night >= @check_in AND night < @check_out;

-- name: ReleaseRoomNights :exec
UPDATE room_nights
SET held_rooms = held_rooms - 1, updated_at = now()
WHERE room_type_id = @room_type_id AND night >= @check_in AND night < @check_out;
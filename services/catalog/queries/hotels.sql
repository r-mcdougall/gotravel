-- name: CreateHotel :one
INSERT INTO hotels (name, city_code, address, star_rating)
VALUES (@name, @city_code, @address, @star_rating)
RETURNING *;

-- name: CreateRoomType :one
INSERT INTO room_types (hotel_id, name, max_guests, total_rooms, price_per_night)
VALUES (@hotel_id, @name, @max_guests, @total_rooms, @price_per_night)
RETURNING *;

-- name: ListRoomTypesByCity :many
SELECT sqlc.embed(hotels), sqlc.embed(room_types)
FROM hotels
JOIN room_types ON room_types.hotel_id = hotels.id
WHERE hotels.city_code = @city_code
  AND room_types.max_guests >= @guests
ORDER BY hotels.id, room_types.price_per_night, room_types.id;

-- name: ListAirlines :many
SELECT * FROM airlines ORDER BY code;

-- name: ListAirports :many
SELECT * FROM airports ORDER BY code;

-- name: ListCities :many
SELECT * FROM cities ORDER BY code;

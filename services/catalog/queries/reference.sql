-- name: ListAirlines :many
SELECT * FROM airlines ORDER BY code;

-- name: ListAirports :many
SELECT * FROM airports ORDER BY code;

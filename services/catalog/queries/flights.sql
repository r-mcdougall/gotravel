-- name: CreateFlight :one
INSERT INTO flights (
    flight_number, airline_code, origin_code, destination_code,
    departure_at, arrival_at, price, capacity, available_seats
)
VALUES (
    @flight_number, @airline_code, @origin_code, @destination_code,
    @departure_at, @arrival_at, @price, @capacity, @available_seats
)
RETURNING *;

-- name: GetFlight :one
SELECT * FROM flights WHERE id = @id;

-- name: GetFlightForUpdate :one
SELECT * FROM flights WHERE id = @id FOR UPDATE;

-- name: UpdateFlight :one
UPDATE flights
SET departure_at = @departure_at,
    arrival_at = @arrival_at,
    price = @price,
    capacity = @capacity,
    available_seats = @available_seats,
    version = version + 1,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: ListFlightsByRouteDay :many
SELECT * FROM flights
WHERE origin_code = @origin_code
  AND destination_code = @destination_code
  AND departure_at >= @day_start
  AND departure_at < @day_end
ORDER BY departure_at, id;

-- name: ListFlights :many
SELECT * FROM flights
WHERE (sqlc.narg('origin_code')::TEXT IS NULL OR origin_code = sqlc.narg('origin_code'))
  AND (sqlc.narg('destination_code')::TEXT IS NULL OR destination_code = sqlc.narg('destination_code'))
  AND (sqlc.narg('departure_date')::DATE IS NULL OR
       (departure_at AT TIME ZONE (SELECT a.timezone FROM airports a WHERE a.code = flights.origin_code))::DATE
           = sqlc.narg('departure_date'))
ORDER BY departure_at DESC, id
LIMIT @row_limit OFFSET @row_offset;

-- name: CountFlights :one
SELECT count(*) FROM flights
WHERE (sqlc.narg('origin_code')::TEXT IS NULL OR origin_code = sqlc.narg('origin_code'))
  AND (sqlc.narg('destination_code')::TEXT IS NULL OR destination_code = sqlc.narg('destination_code'))
  AND (sqlc.narg('departure_date')::DATE IS NULL OR
       (departure_at AT TIME ZONE (SELECT a.timezone FROM airports a WHERE a.code = flights.origin_code))::DATE
           = sqlc.narg('departure_date'));
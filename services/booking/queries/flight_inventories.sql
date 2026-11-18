-- name: UpsertFlightInventory :one
INSERT INTO flight_inventories (
    flight_id, version, flight_number, airline_code, origin_code, destination_code,
    departure_at, arrival_at, price, capacity, availability_version
) VALUES (
     @flight_id, @version, @flight_number, @airline_code, @origin_code, @destination_code,
     @departure_at, @arrival_at, @price, @capacity, 1
)
ON CONFLICT (flight_id) DO UPDATE SET
    version              = EXCLUDED.version,
    flight_number        = EXCLUDED.flight_number,
    airline_code         = EXCLUDED.airline_code,
    origin_code          = EXCLUDED.origin_code,
    destination_code     = EXCLUDED.destination_code,
    departure_at         = EXCLUDED.departure_at,
    arrival_at           = EXCLUDED.arrival_at,
    price                = EXCLUDED.price,
    capacity             = EXCLUDED.capacity,
    availability_version = flight_inventories.availability_version + 1,
    updated_at           = now()
WHERE flight_inventories.version < EXCLUDED.version
RETURNING *;

-- name: GetFlightInventory :one
SELECT * FROM flight_inventories WHERE flight_id = @flight_id;
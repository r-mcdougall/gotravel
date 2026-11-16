-- name: UpsertFlightInventory :exec
INSERT INTO flight_inventories (
    flight_id, version, flight_number, airline_code, origin_code,
    destination_code, departure_at, arrival_at, price, capacity
) VALUES (
    @flight_id, @version, @flight_number, @airline_code, @origin_code,
    @destination_code, @departure_at, @arrival_at, @price, @capacity
)
ON CONFLICT (flight_id) DO UPDATE SET
    version          = EXCLUDED.version,
    flight_number    = EXCLUDED.flight_number,
    airline_code     = EXCLUDED.airline_code,
    origin_code      = EXCLUDED.origin_code,
    destination_code = EXCLUDED.destination_code,
    departure_at     = EXCLUDED.departure_at,
    arrival_at       = EXCLUDED.arrival_at,
    price            = EXCLUDED.price,
    capacity         = EXCLUDED.capacity,
    updated_at       = now()
WHERE flight_inventories.version < EXCLUDED.version;

-- name: GetFlightInventory :one
SELECT * FROM flight_inventories WHERE flight_id = @flight_id;
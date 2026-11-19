-- name: SummarizeBookingsByStatus :many
SELECT
    status,
    count(*) AS bookings,
    sum(seat_price * passenger_count)::bigint AS amount
FROM bookings
WHERE created_at >= @created_from AND created_at < @created_to
GROUP BY status;

-- name: ListTopRoutes :many
SELECT
    f.origin_code,
    f.destination_code,
    count(*) AS bookings,
    sum(b.passenger_count)::bigint AS seats,
    sum(b.seat_price * b.passenger_count)::bigint AS revenue
FROM bookings b
JOIN flight_inventories f ON f.flight_id = b.flight_id
WHERE b.status = 'CONFIRMED' AND b.created_at >= @created_from AND b.created_at < @created_to
GROUP BY f.origin_code, f.destination_code
ORDER BY bookings DESC, seats DESC, f.origin_code, f.destination_code
LIMIT @max_routes;

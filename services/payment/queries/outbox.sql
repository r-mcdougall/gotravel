-- name: InsertOutboxEvent :exec
INSERT INTO outbox (event_type, payload) VALUES (@event_type, @payload);

-- name: CreatePayment :one
INSERT INTO payments (booking_id, user_id, amount, status)
VALUES (@booking_id, @user_id, @amount, @status)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments WHERE id = @id AND user_id = @user_id;

-- name: ReversePayment :one
UPDATE payments
SET status = 'REVERSED', updated_at = now()
WHERE id = @id AND status = 'SUCCEEDED'
RETURNING *;

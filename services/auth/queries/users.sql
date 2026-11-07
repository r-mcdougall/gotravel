-- name: CreateUser :one
INSERT INTO users (email, password_hash, full_name, phone, role)
VALUES (@email, @password_hash, @full_name, sqlc.narg('phone')::TEXT, @role)
RETURNING *;

-- name: CreateAdminIfAbsent :execrows
INSERT INTO users (email, password_hash, full_name, role)
VALUES (@email, @password_hash, @full_name, 'admin')
ON CONFLICT (email) DO NOTHING;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = @email;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: UpdateUserProfile :one
UPDATE users
SET full_name = COALESCE(sqlc.narg('full_name')::TEXT, full_name),
    phone = CASE WHEN @set_phone::BOOLEAN THEN sqlc.narg('phone')::TEXT ELSE phone END,
    updated_at = NOW()
WHERE id = @id
RETURNING *;

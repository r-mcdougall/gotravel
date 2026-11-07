-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES (@user_id, @token_hash, @expires_at);

-- name: RotateRefreshToken :one
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = @token_hash
  AND revoked_at IS NULL
  AND expires_at > NOW()
RETURNING user_id;

-- name: GetRefreshToken :one
SELECT * FROM refresh_tokens WHERE token_hash = @token_hash;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = @token_hash AND revoked_at IS NULL;

-- name: RevokeAllRefreshTokens :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE user_id = @user_id AND revoked_at IS NULL;

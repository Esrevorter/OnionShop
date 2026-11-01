-- db/query/auth_phase3.sql — Phase 3 auth surface queries (migration 0006).
-- Password hashes and personal phrases never appear in any :many listing;
-- these are point lookups used exclusively by the auth handlers.

-- name: RegisterUser :one
INSERT INTO users (username, password_hash)
VALUES ($1, $2)
RETURNING *;

-- name: SetUserPGPPublicKey :exec
UPDATE users SET pgp_public_key = $2, updated_at = now()
WHERE id = $1;

-- name: SetUserPersonalPhrase :exec
UPDATE users SET personal_phrase = $2, updated_at = now()
WHERE id = $1;

-- name: SetUserTier :exec
UPDATE users SET tier = $2, updated_at = now()
WHERE id = $1;

-- name: RecordLoginAttempt :exec
INSERT INTO login_history (user_id, success, host_used)
VALUES ($1, $2, $3);

-- name: RecentLoginHistory :many
SELECT id, user_id, success, host_used, created_at FROM login_history
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 20;

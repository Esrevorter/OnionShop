-- db/query/users.sql — authentication & account lookups (Phase 3 handlers).
-- Column contract: migration 0001 (users table). password_hash stores an
-- argon2id encoded hash; never log or return it (AGENTS.md §4).

-- name: CreateUser :one
INSERT INTO users (username, password_hash, role, pgp_public_key, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: UpdateUserPasswordHash :exec
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserRole :exec
UPDATE users SET role = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserStatus :exec
UPDATE users SET status = $2, updated_at = now() WHERE id = $1;

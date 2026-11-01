-- db/query/sessions.sql — sqlc input for the session store (internal/store/gen).
--
-- These are the canonical forms of the statements executed by
-- internal/store/sessions.go. Keeping them here means `sqlc generate` verifies
-- every session query against db/migrations at CI time, and Phase 3+ handlers
-- can adopt the generated typed wrappers instead of hand-written SQL.
--
-- Column contract (migration 0001 + 0005):
--   id            TEXT PRIMARY KEY      -- scs token (base64url, >=128-bit entropy)
--   user_id       UUID NULL             -- set on login, cleared on logout
--   data          BYTEA                 -- scs gob-encoded payload
--   expires_at    TIMESTAMPTZ           -- idle expiry, refreshed on each commit
--   absolute_stop TIMESTAMPTZ           -- hard lifetime cap, never extended
--   updated_at    TIMESTAMPTZ           -- last write (added by 0005); used for warm-back order

-- name: FindSession :one
SELECT data FROM sessions
WHERE id = $1 AND expires_at > now() AND absolute_stop > now();

-- name: CommitSession :exec
INSERT INTO sessions (id, data, expires_at, absolute_stop, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (id) DO UPDATE
    SET data          = EXCLUDED.data,
        expires_at    = EXCLUDED.expires_at,
        absolute_stop = LEAST(sessions.absolute_stop, EXCLUDED.absolute_stop),
        updated_at    = now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: CleanupSessions :exec
DELETE FROM sessions WHERE expires_at < now();

-- name: CountActiveUserSessions :one
SELECT count(*) AS active_users FROM sessions
WHERE user_id IS NOT NULL AND expires_at > now() AND absolute_stop > now();

-- name: TouchableSessions :many
-- Live rows ordered newest-first, for warming Redis back up after a flush.
-- The application clamps each TTL to absolute_stop before writing.
SELECT id, data, expires_at, absolute_stop FROM sessions
WHERE expires_at > now() AND absolute_stop > now()
ORDER BY updated_at DESC NULLS LAST
LIMIT $1;

-- name: SetSessionUser :exec
UPDATE sessions SET user_id = $2, updated_at = now() WHERE id = $1;

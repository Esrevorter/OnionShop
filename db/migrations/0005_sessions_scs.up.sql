-- 0005_sessions_scs.sql — adapt the 0001 `sessions` table to the scs session
-- store used by internal/store (Redis primary, Postgres fallback;
-- ARCHITECTURE.md §2). scs writes: token (TEXT), gob payload (BYTEA), idle
-- expiry (TIMESTAMPTZ). absolute_stop keeps the original hard deadline so an
-- idle refresh can never extend a session past its lifetime cap.
BEGIN;

-- scs tokens are base64url strings, not BYTEA. The legacy column had no rows
-- in any deployed environment (Phase 2 is the first code that touches it);
-- using USING eliminates rather than attempting an impossible cast.
ALTER TABLE sessions
    ALTER COLUMN id TYPE TEXT USING id::text;

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS expiry TIMESTAMPTZ NOT NULL DEFAULT now();

-- One live row per token. Partial index: expired rows stay cheap for the
-- cleanup cron instead of fighting over uniqueness.
CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_live_idx
    ON sessions (id) WHERE expires_at > now();

COMMENT ON COLUMN sessions.expiry IS 'scs idle expiry (refreshed on each commit)';
COMMENT ON COLUMN sessions.absolute_stop IS 'hard lifetime cap set at creation; never extended';

COMMIT;

-- 0005_sessions_scs.sql — adapt the legacy 0001 `sessions` table to the scs
-- session store implemented in internal/store (Redis fast path, Postgres
-- durable fallback; ARCHITECTURE.md §2).
--
-- What scs needs that 0001 did not provide:
--   * a TEXT primary key (scs tokens are base64url strings, not raw BYTEA);
--   * an opaque payload column (scs gob-encodes its data map; JSONB would
--     reject it) -> BYTEA;
--   * a last-write timestamp so Redis warm-back can prefer hot sessions.
--
-- Expiry model (unchanged from 0001, deliberately):
--   expires_at    = idle deadline, rewritten on every commit (sliding window);
--   absolute_stop = hard lifetime cap set at creation, clamped with LEAST() so
--                   refreshing can never extend a session's life.
BEGIN;

-- scs tokens are base64url strings. The legacy column had no rows in any
-- deployed environment (this migration is the first code that touches it), so
-- USING encode() rather than attempting an impossible bytea->text cast.
ALTER TABLE sessions
    ALTER COLUMN id TYPE TEXT USING encode(id, 'hex');

ALTER TABLE sessions
    ALTER COLUMN data TYPE BYTEA USING convert_to(data::text, 'UTF8'),
    ALTER COLUMN data DROP DEFAULT;

-- Last-write marker used by FallbackStore.TouchAll (ORDER BY updated_at DESC).
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

COMMENT ON COLUMN sessions.expires_at IS 'scs idle expiry (refreshed on each commit)';
COMMENT ON COLUMN sessions.absolute_stop IS 'hard lifetime cap set at creation; never extended';
COMMENT ON COLUMN sessions.data IS 'scs gob-encoded session payload';

COMMIT;

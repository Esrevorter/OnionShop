-- 0005_sessions_scs.down.sql — revert the scs adaptations back to the 0001 shape.
BEGIN;

DROP INDEX IF EXISTS sessions_token_live_idx;

ALTER TABLE sessions DROP COLUMN IF EXISTS updated_at;

-- Drop every row written by scs (tokens are base64url text and cannot be cast
-- to the legacy BYTEA primary key); then restore the original column types.
DELETE FROM sessions WHERE id !~ '^[0-9a-fA-Fx]+$';

ALTER TABLE sessions
    ALTER COLUMN data TYPE JSONB USING '{}'::jsonb,
    ALTER COLUMN data SET DEFAULT '{}'::jsonb;

ALTER TABLE sessions
    ALTER COLUMN id TYPE BYTEA USING decode(replace(replace(id, '-', ''), '_', ''), 'base64');

COMMIT;

-- 0006_auth_phase3 down — drop Phase 3 additions (users.personal_phrase and
-- the login_history table). Reversible: no data depended on them yet.
BEGIN;

DROP TABLE IF EXISTS login_history;
ALTER TABLE users DROP COLUMN IF EXISTS personal_phrase;

COMMIT;

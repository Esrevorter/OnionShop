DROP TABLE IF EXISTS login_history;
ALTER TABLE users
    DROP COLUMN IF EXISTS personal_phrase,
    DROP COLUMN IF EXISTS tier;

-- 0006_auth_phase3.sql — Phase 3 auth & RBAC support columns/tables.
-- personal_phrase: anti-phishing display phrase (ARCHITECTURE.md §9) shown on
--   every authenticated page so users can sanity-check they are on the real
--   site; NOT a credential, never used for authentication decisions.
-- login_history: last-N logins per ARCHITECTURE.md §9 — UTC time + the Host
--   header / mirror the request arrived on. Deliberately NO IP column: all
--   traffic is 127.0.0.1 through Tor and IPs must never be stored (AGENTS.md §1).
BEGIN;

ALTER TABLE users ADD COLUMN personal_phrase TEXT
    CHECK (personal_phrase IS NULL OR char_length(personal_phrase) BETWEEN 4 AND 64);

CREATE TABLE login_history (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- UTC from Postgres, never client clocks
    host_header TEXT NOT NULL DEFAULT ''             -- onion hostname the login rode
);
CREATE INDEX login_history_user_idx ON login_history (user_id, occurred_at DESC);

COMMIT;

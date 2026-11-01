-- 0006_auth_phase3 — Phase 3 auth surface: account tiers, personal phrases,
-- and the login-history table. See SECURITY_AND_OPSEC.md §1 (anti-phishing)
-- and AGENTS.md §4 (Argon2id hashes already live in users.password_hash).

ALTER TABLE users
    ADD COLUMN tier TEXT NOT NULL DEFAULT 'basic'
        CHECK (tier IN ('basic','upgraded')),
    ADD COLUMN personal_phrase TEXT;  -- short anti-phishing phrase shown at login

-- Login history deliberately stores NO IP addresses: only the Host header the
-- request was served under (helps users spot lookalike onion clones) and a
-- success flag. Timestamps are server-side UTC via now().
CREATE TABLE login_history (
    id         BIGSERIAL PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    success    BOOLEAN NOT NULL,
    host_used  TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX login_history_user_idx ON login_history (user_id, created_at DESC);

-- 0003_ledger_audit.sql — append-only double-entry ledger (ARCHITECTURE.md §6),
-- withdrawals, and the tamper-evident audit trail (SECURITY_AND_OPSEC.md §3).
BEGIN;

CREATE TABLE accounts (
    id      SERIAL PRIMARY KEY,
    name    TEXT NOT NULL UNIQUE      -- 'escrow', 'platform_fee', 'refund', 'vendor_<uuid>'
        CHECK (name ~ '^(escrow|platform_fee|refund|vendor_[0-9a-f-]{36})$'),
    kind    TEXT NOT NULL CHECK (kind IN ('asset','liability','revenue')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO accounts (name, kind) VALUES
    ('escrow', 'asset'), ('platform_fee', 'revenue'), ('refund', 'liability');

-- Immutable entries: balance = SUM(debits) - SUM(credits) per account.
CREATE TABLE ledger_entries (
    id             BIGSERIAL PRIMARY KEY,
    tx_ref         UUID NOT NULL,               -- one logical movement shares a tx_ref
    idempotency_key TEXT NOT NULL UNIQUE,       -- e.g. 'pay:<invoice_id>', 'release:<order_id>'
    account_id     INT NOT NULL REFERENCES accounts(id),
    amount_sat     BIGINT NOT NULL CHECK (amount_sat > 0),
    direction      TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    order_id       UUID REFERENCES orders(id),
    memo           TEXT NOT NULL DEFAULT '' CHECK (char_length(memo) <= 500),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ledger_account_idx ON ledger_entries (account_id, id);
CREATE INDEX ledger_tx_idx      ON ledger_entries (tx_ref);

-- Guard: entries are append-only. UPDATE/DELETE require breaking this trigger.
CREATE OR REPLACE FUNCTION forbid_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only (%) is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();

CREATE VIEW account_balances AS
    SELECT account_id,
           COALESCE(SUM(amount_sat) FILTER (WHERE direction = 'debit'),  0)
         - COALESCE(SUM(amount_sat) FILTER (WHERE direction = 'credit'), 0) AS balance_sat
    FROM ledger_entries
    GROUP BY account_id;

CREATE TABLE withdrawals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id    UUID NOT NULL REFERENCES vendors(id),
    amount_sat   BIGINT NOT NULL CHECK (amount_sat > 0),
    btc_address  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'requested'
                 CHECK (status IN ('requested','approved','paid','rejected')),
    reviewed_by  UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Audit trail: every privileged/system action. Rows are immutable like the ledger.
CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    actor_id   UUID REFERENCES users(id),        -- NULL => automated cron (actor='system')
    actor_kind TEXT NOT NULL DEFAULT 'user' CHECK (actor_kind IN ('user','system')),
    action     TEXT NOT NULL,                    -- 'dispute.resolve','vendor.approve',...
    target     TEXT NOT NULL,                    -- '<table>:<id>'
    details    JSONB NOT NULL DEFAULT '{}',      -- NO secrets/PII here (log sanitization rules)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_target_idx ON audit_log (target, created_at);

COMMIT;

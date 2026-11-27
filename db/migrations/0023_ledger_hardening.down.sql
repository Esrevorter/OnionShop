-- Reverse 0023. Restores the pre-0009-style view definition only for symmetry;
-- note the "down" state's view references amount_sat, which no longer exists on
-- migrated databases after 0009 — downgrading past 0009 was already unsupported,
-- so this file simply removes what 0023 added.
BEGIN;

DROP TRIGGER IF EXISTS ledger_double_entry ON ledger_entries;
DROP FUNCTION IF EXISTS assert_ledger_balanced();

-- Undo only the rows this migration's backfill inserted (tagged idempotency
-- keys). NOTE: the append-only guard would block a manual DELETE, so we drop
-- it first and re-create it below — mirroring what 0003/schema.sql define.
DROP TRIGGER IF EXISTS ledger_append_only ON ledger_entries;
DELETE FROM ledger_entries WHERE idempotency_key LIKE '0023-backfill:%';

-- Restore the append-only guard that 0003 (and schema.sql) define.
CREATE OR REPLACE FUNCTION forbid_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only (%) is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();

DROP VIEW IF EXISTS account_balances;

COMMIT;

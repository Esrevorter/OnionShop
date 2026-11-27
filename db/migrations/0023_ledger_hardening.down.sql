-- Reverse 0023. Restores the pre-0009-style view definition only for symmetry;
-- note the "down" state's view references amount_sat, which no longer exists on
-- migrated databases after 0009 — downgrading past 0009 was already unsupported,
-- so this file simply removes what 0023 added.
BEGIN;

DROP TRIGGER IF EXISTS ledger_double_entry ON ledger_entries;
DROP FUNCTION IF EXISTS assert_ledger_balanced();
DROP TRIGGER IF EXISTS ledger_append_only ON ledger_entries;
DROP FUNCTION IF EXISTS forbid_ledger_mutation();
DROP VIEW IF EXISTS account_balances;

COMMIT;

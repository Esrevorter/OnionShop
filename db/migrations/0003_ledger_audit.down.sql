BEGIN;
DROP TABLE IF EXISTS audit_log, withdrawals, account_balances, ledger_entries, accounts CASCADE;
DROP FUNCTION IF EXISTS forbid_ledger_mutation() CASCADE;
COMMIT;

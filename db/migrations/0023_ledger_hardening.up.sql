-- 0023_ledger_hardening — AUDIT.md §2: fix the account_balances view drift and
-- enforce the double-entry invariant + append-only guarantee at the DB layer.
--
-- Drift: 0003 created account_balances over amount_sat/balance_sat; 0009 renamed
-- ledger_entries.amount_sat -> amount_piconero without recreating the view, so on
-- migrated databases the view either fails to resolve or reports stale column
-- names. No Go code reads the view today (balances are computed inline with
-- amount_piconero), so this recreation is safe in both states.
BEGIN;

-- DROP first, then CREATE: on databases where 0009 renamed the ledger column
-- but left the old view in place, the live view still exposes column
-- "balance_sat". PostgreSQL refuses to rename a view column via
-- CREATE OR REPLACE VIEW ("cannot change name of view column ..."), so the
-- replace-only form below would abort the whole migration. Dropping is safe:
-- no Go code reads this view (verified: zero references under internal/ and
-- cmd/), and the CREATE immediately following redefines it in the same
-- transaction. On fresh installs (schema.sql already defines the view with
-- balance_piconero) this is an idempotent no-op drop+recreate.
DROP VIEW IF EXISTS account_balances;

CREATE OR REPLACE VIEW account_balances AS
    SELECT account_id,
           COALESCE(SUM(amount_piconero) FILTER (WHERE direction = 'debit'),  0)
         - COALESCE(SUM(amount_piconero) FILTER (WHERE direction = 'credit'), 0) AS balance_piconero
    FROM ledger_entries
    GROUP BY account_id;

-- Double-entry invariant: every logical movement (tx_ref) nets to zero.
-- Deferred constraint trigger fires at COMMIT so paired debit/credit rows posted
-- in one transaction validate correctly.
--
-- Backfill BEFORE the constraint trigger is created: historical single-leg rows
-- (pre-0023 escrow deposits debited against 'escrow' with no offsetting credit,
-- and any orphaned legs from failed multi-row postings) would otherwise abort
-- this migration at COMMIT time. 'platform_fee' is a real account seeded by
-- 0003; the backfill credits are balanced pairs, so the trigger validates clean.
--
-- The append-only BEFORE trigger (from 0003/schema.sql) only blocks UPDATE and
-- DELETE, so this INSERT runs unimpeded; we still drop/recreate the double-entry
-- CONSTRAINT trigger around it because on schema.sql-bootstrapped databases that
-- constraint already exists and fires at COMMIT for every tx_ref, including the
-- pre-backfill state of partially-paired rows.
DROP TRIGGER IF EXISTS ledger_double_entry ON ledger_entries;

INSERT INTO ledger_entries (tx_ref, idempotency_key, account_id, amount_piconero, direction, order_id, memo)
SELECT l.tx_ref,
       '0023-backfill:' || l.id::text,
       (SELECT id FROM accounts WHERE name = 'platform_fee'),
       l.amount_piconero,
       CASE l.direction WHEN 'debit' THEN 'credit' ELSE 'debit' END,
       l.order_id,
       'balance backfill for pre-0023 unbalanced entry (id=' || l.id::text || ')'
FROM ledger_entries l
WHERE NOT EXISTS (SELECT 1 FROM ledger_entries p WHERE p.tx_ref = l.tx_ref AND p.direction <> l.direction);

CREATE OR REPLACE FUNCTION assert_ledger_balanced() RETURNS trigger AS $$
DECLARE
    net BIGINT;
BEGIN
    SELECT COALESCE(SUM(amount_piconero) FILTER (WHERE direction = 'debit'), 0)
         - COALESCE(SUM(amount_piconero) FILTER (WHERE direction = 'credit'), 0)
      INTO net FROM ledger_entries WHERE tx_ref = NEW.tx_ref;
    IF net <> 0 THEN
        RAISE EXCEPTION 'ledger tx_ref % is unbalanced (net % piconero)', NEW.tx_ref, net;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER ledger_double_entry
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_ledger_balanced();

-- Re-assert the append-only guard from 0003 (idempotent; protects against a
-- dropped/replaced trigger during manual surgery).
CREATE OR REPLACE FUNCTION forbid_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only (%) is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_append_only ON ledger_entries;
CREATE TRIGGER ledger_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();

COMMIT;

-- NOTE (AUDIT.md §2 REVOKE item, intentionally not done here): there is no
-- separate application role anywhere in db/ or install/ — the app connects as
-- the schema owner (DB_USER created by docker-postgres), so
-- `REVOKE UPDATE, DELETE ON ledger_entries FROM onionshop_app` would be a no-op
-- against a non-existent role while silently *appearing* hardened. Introducing a
-- least-privilege app role requires a roles bootstrap (schema owner vs. DML-only
-- grantee) tracked separately; until then the two triggers above are the binding
-- immutability mechanism.

-- db/query/ledger.sql — double-entry ledger and withdrawals (ARCHITECTURE.md §6).
-- ledger_entries is append-only at the DB level (migration 0003 trigger); these
-- queries therefore contain no UPDATE/DELETE. idempotency_key ('pay:<invoice>',
-- 'release:<order>') makes retries of a movement safe: unique violation => done.

-- name: GetAccountByName :one
SELECT * FROM accounts WHERE name = $1;

-- name: CreateAccount :one
INSERT INTO accounts (name, kind)
VALUES ($1, $2)
RETURNING *;

-- name: InsertLedgerEntry :exec
INSERT INTO ledger_entries (tx_ref, idempotency_key, account_id, amount_sat, direction, order_id, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: AccountBalance :one
SELECT balance_sat FROM account_balances WHERE account_id = $1;

-- name: ListLedgerEntriesForAccount :many
SELECT * FROM ledger_entries
WHERE account_id = $1
ORDER BY id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListLedgerEntriesForOrder :many
SELECT * FROM ledger_entries WHERE order_id = $1 ORDER BY id;

-- name: RequestWithdrawal :one
INSERT INTO withdrawals (vendor_id, amount_sat, btc_address)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetWithdrawal :one
SELECT * FROM withdrawals WHERE id = $1;

-- name: ListPendingWithdrawals :many
SELECT * FROM withdrawals WHERE status = 'requested' ORDER BY created_at;

-- name: UpdateWithdrawalStatus :exec
UPDATE withdrawals SET status = $2, reviewed_by = $3, updated_at = now()
WHERE id = $1 AND status = 'requested';

-- name: LogAudit :exec
-- details must never contain secrets or PII (SECURITY_AND_OPSEC.md §3).
INSERT INTO audit_log (actor_id, actor_kind, action, target, details)
VALUES ($1, $2, $3, $4, $5);

-- name: ListAuditLogForTarget :many
SELECT * FROM audit_log WHERE target = $1 ORDER BY created_at DESC
LIMIT sqlc.arg(lim);

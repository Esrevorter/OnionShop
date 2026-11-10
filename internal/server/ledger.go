package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LedgerService handles append-only double-entry credit/debit movements within
// explicit PostgreSQL transactions (ARCHITECTURE.md §6). Every financial event
// must be executed atomically with an idempotency key.
type LedgerService struct {
	pool *pgxpool.Pool
}

func NewLedgerService(pool *pgxpool.Pool) *LedgerService {
	return &LedgerService{pool: pool}
}

// LedgerLeg represents one side (debit or credit) of a double-entry line.
type LedgerLeg struct {
	AccountName string
	AmountSat   int64
	Direction   string // "debit" or "credit"
}

// RecordLedgerTx executes a balanced double-entry transaction atomically.
func (s *LedgerService) RecordLedgerTx(ctx context.Context, txRef pgtype.UUID, idempotencyKey string, orderID *pgtype.UUID, memo string, legs []LedgerLeg) error {
	if len(legs) == 0 {
		return errors.New("ledger: empty transaction legs")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ledger: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify balance: sum(debits) must equal sum(credits)
	var totalDebit, totalCredit int64
	for _, leg := range legs {
		if leg.AmountSat <= 0 {
			return errors.New("ledger: non-positive amount")
		}
		switch leg.Direction {
		case "debit":
			totalDebit += leg.AmountSat
		case "credit":
			totalCredit += leg.AmountSat
		default:
			return fmt.Errorf("ledger: invalid direction %q", leg.Direction)
		}
	}
	if totalDebit != totalCredit {
		return fmt.Errorf("ledger: unbalanced transaction: debits (%d) != credits (%d)", totalDebit, totalCredit)
	}

	for _, leg := range legs {
		// Resolve account ID by name
		var accountID int
		err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE name = $1`, leg.AccountName).Scan(&accountID)
		if err != nil {
			return fmt.Errorf("ledger: resolve account %q: %w", leg.AccountName, err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO ledger_entries (tx_ref, idempotency_key, account_id, amount_sat, direction, order_id, memo)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, txRef, idempotencyKey, accountID, leg.AmountSat, leg.Direction, orderID, memo)
		if err != nil {
			return fmt.Errorf("ledger: insert entry: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ledger: commit tx: %w", err)
	}
	return nil
}

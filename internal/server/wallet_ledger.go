package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func vendorAccountName(vendorID pgtype.UUID) string { return "vendor_" + uuidString(vendorID) }

func ensureVendorAccount(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) (int, error) {
	name := vendorAccountName(vendorID)
	_, err := pool.Exec(ctx, `INSERT INTO accounts (name, kind) VALUES ($1,'liability') ON CONFLICT (name) DO NOTHING`, name)
	if err != nil {
		return 0, fmt.Errorf("server: ensure vendor account: %w", err)
	}
	var id int
	if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE name=$1`, name).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func PostEscrowRelease(ctx context.Context, pool *pgxpool.Pool, vendorID, orderID pgtype.UUID, piconero int64) error {
	if piconero <= 0 { return nil }
	accID, err := ensureVendorAccount(ctx, pool, vendorID)
	if err != nil { return err }
	var escrowID int
	if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE name='escrow'`).Scan(&escrowID); err != nil {
		return err
	}
	var txref pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&txref); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO ledger_entries (tx_ref, idempotency_key, account_id, amount_piconero, direction, order_id, memo)
		VALUES ($1,'release:'||cast($2 as text), $3, $4, 'debit', $2, 'escrow released to vendor'),
		       ($1,'vendor-credit:'||cast($2 as text), $5, $4, 'credit', $2, 'vendor balance credited')
		ON CONFLICT (idempotency_key) DO NOTHING`,
		txref, orderID, escrowID, piconero, accID)
	if err != nil {
		return fmt.Errorf("server: post release: %w", err)
	}
	return nil
}

func PostEscrowRefund(ctx context.Context, pool *pgxpool.Pool, orderID pgtype.UUID, piconero int64) error {
	if piconero <= 0 { return nil }
	var escrowID, refundID int
	if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE name='escrow'`).Scan(&escrowID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE name='refund'`).Scan(&refundID); err != nil {
		return err
	}
	var txref pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&txref); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO ledger_entries (tx_ref, idempotency_key, account_id, amount_piconero, direction, order_id, memo)
		VALUES ($1,'refund:'||cast($2 as text), $3, $4, 'debit', $2, 'escrow released for refund'),
		       ($1,'refund-pool:'||cast($2 as text), $5, $4, 'credit', $2, 'refund returned to pool')
		ON CONFLICT (idempotency_key) DO NOTHING`,
		txref, orderID, escrowID, piconero, refundID)
	if err != nil {
		return fmt.Errorf("server: post refund: %w", err)
	}
	return nil
}

func VendorBalancePiconero(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) int64 {
	var bal int64
	_ = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_piconero) FILTER (WHERE direction='credit'),0)
		     - COALESCE(SUM(amount_piconero) FILTER (WHERE direction='debit'),0)
		FROM ledger_entries
		WHERE account_id = (SELECT id FROM accounts WHERE name=$1)`,
		vendorAccountName(vendorID)).Scan(&bal)
	return bal
}

func VendorPendingPiconero(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) int64 {
	var v int64
	_ = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(xmr_amount_piconero),0) FROM orders
		WHERE vendor_id=$1 AND status IN ('in_escrow','shipped','disputed')`, vendorID).Scan(&v)
	return v
}

type LedgerRow struct {
	Memo      string
	Direction string
	Amount    int64
	CreatedAt string
}

func VendorLedgerHistory(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID, limit int) []LedgerRow {
	rows, err := pool.Query(ctx, `
		SELECT memo, direction, amount_piconero, created_at
		FROM ledger_entries
		WHERE account_id = (SELECT id FROM accounts WHERE name=$1)
		ORDER BY created_at DESC LIMIT $2`, vendorAccountName(vendorID), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []LedgerRow
	for rows.Next() {
		var l LedgerRow
		var created time.Time
		if err := rows.Scan(&l.Memo, &l.Direction, &l.Amount, &created); err == nil {
			l.CreatedAt = created.Format("2006-01-02 15:04")
			out = append(out, l)
		}
	}
	return out
}

type WalletData struct {
	AuthData
	IsVendor        bool
	BalancePiconero int64
	PendingPiconero int64
	History         []LedgerRow
}

func (a *App) registerWalletRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/wallet", anyUser(http.HandlerFunc(a.handleWalletPage)))
}

func (a *App) handleWalletPage(w http.ResponseWriter, r *http.Request) {
	data := WalletData{AuthData: a.baseData(r)}
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		uid := CurrentUserID(r.Context(), a.sm)
		if uid != "" {
			if u, ok := parseUUID(uid); ok {
				var vid pgtype.UUID
				if err := pool.QueryRow(r.Context(), `SELECT id FROM vendors WHERE user_id=$1 AND approved`, u).Scan(&vid); err == nil {
					data.IsVendor = true
					data.BalancePiconero = VendorBalancePiconero(r.Context(), pool, vid)
					data.PendingPiconero = VendorPendingPiconero(r.Context(), pool, vid)
					data.History = VendorLedgerHistory(r.Context(), pool, vid, 20)
				}
			}
		}
	}
	if data.Captcha = a.newChallenge(w, r, "withdraw"); data.Captcha == nil {
		return
	}
	a.renderAs(w, "wallet", http.StatusOK, data)
}

func pgtypeUUIDFromIface(v interface{}) pgtype.UUID {
	if u, ok := v.(pgtype.UUID); ok {
		return u
	}
	return pgtype.UUID{}
}

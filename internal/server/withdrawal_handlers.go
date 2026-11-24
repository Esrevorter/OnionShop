package server

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// WithdrawalRecord represents a row from the withdrawals table.
type WithdrawalRecord struct {
	ID         pgtype.UUID
	VendorID   pgtype.UUID
	AmountPiconero  int64
	XmrAddress string
	Status     string
	CreatedAt  time.Time
}

// WithdrawalStore defines database operations for withdrawals and wallet balances.
type WithdrawalStore interface {
	VendorGetByUser(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error)
	GetVendorBalanceSat(ctx context.Context, vendorID pgtype.UUID) (int64, error)
	CreateWithdrawalRequest(ctx context.Context, vendorID pgtype.UUID, amountPiconero int64, xmrAddress string) error
	ListWithdrawalsByVendor(ctx context.Context, vendorID pgtype.UUID) ([]WithdrawalRecord, error)
	ListPendingWithdrawals(ctx context.Context) ([]WithdrawalRecord, error)
	ReviewWithdrawal(ctx context.Context, withdrawalID pgtype.UUID, reviewerID pgtype.UUID, status string) error
	GetWithdrawal(ctx context.Context, withdrawalID pgtype.UUID) (*WithdrawalRecord, error)
}

// RegisterWithdrawalRoutes mounts wallet and withdrawal routes.
func (a *App) RegisterWithdrawalRoutes(r chi.Router, store WithdrawalStore) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	adminOnly := a.RequireRole("admin")

	r.Method(http.MethodPost, "/wallet/withdraw", anyUser(http.HandlerFunc(a.handleWithdrawSubmit)))
	r.Method(http.MethodGet, "/admin/withdrawals", adminOnly(http.HandlerFunc(a.handleAdminWithdrawals)))
	r.Method(http.MethodPost, "/admin/withdrawals/{id}/review", adminOnly(http.HandlerFunc(a.handleAdminReviewWithdrawal)))
}

func (a *App) handleWallet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := a.baseData(r)
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil {
		writeGenericError(w)
		return
	}

	// Check if user is a vendor
	if a.auth != nil {
		if v, _ := a.auth.VendorGetByUser(ctx, *uid); v != nil && v.Approved {
			data.VendorState = v
		}
	}

	a.render(w, "wallet", http.StatusOK, data)
}

func (a *App) handleWithdrawSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil { writeGenericError(w); return }
	fail := func(msg string) { a.renderWalletPage(w, r, http.StatusUnprocessableEntity, msg, "") }
	if honeypotTriggered(r) || !a.requireFriction(w, r, "withdraw") {
		fail("Human verification failed, please try again.")
		return
	}
	if a.auth == nil { fail("service unavailable"); return }
	v, err := a.auth.VendorGetByUser(ctx, *uid)
	if err != nil || v == nil || !v.Approved { fail("only approved vendors can request withdrawals"); return }
	amountPiconero, err := parseDecimalUnits(r.FormValue("amount_piconero"), 12)
	if err != nil { fail("invalid withdrawal amount: " + err.Error()); return }
	xmrAddress := strings.TrimSpace(r.FormValue("xmr_address"))
	if xmrAddress == "" { fail("monero address is required"); return }
	ws, ok := a.vendor.(WithdrawalStore)
	if !ok { fail("withdrawal service unavailable"); return }
	if err := ws.CreateWithdrawalRequest(ctx, v.ID, amountPiconero, xmrAddress); err != nil {
		fail("could not create withdrawal request: " + err.Error())
		return
	}
	a.renderWalletPage(w, r, http.StatusOK, "", "Withdrawal request submitted (subject to review).")
}

type AdminWithdrawalView struct {
	IDStr      string
	VendorName string
	AmountPico int64
	XmrAddress string
	Status     string
	CreatedAt  string
}

type AdminWithdrawalsData struct {
	AuthData
	Withdrawals []AdminWithdrawalView
	Error       string
}

func (a *App) handleAdminWithdrawals(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		writeGenericError(w)
		return
	}
	data := AdminWithdrawalsData{AuthData: a.baseData(r)}
	
	rows, err := pool.Query(r.Context(), `
		SELECT w.id, u.username, w.amount_piconero, w.xmr_address, w.status, w.created_at
		FROM withdrawals w 
		JOIN vendors v ON v.id = w.vendor_id 
		JOIN users u ON u.id = v.user_id
		ORDER BY w.created_at DESC`)
	if err != nil {
		data.Error = "Could not load withdrawals"
	} else {
		defer rows.Close()
		for rows.Next() {
			var wd AdminWithdrawalView
			var id pgtype.UUID
			var created time.Time
			if err := rows.Scan(&id, &wd.VendorName, &wd.AmountPico, &wd.XmrAddress, &wd.Status, &created); err == nil {
				wd.IDStr = uuidString(id)
				wd.CreatedAt = created.Format("2006-01-02 15:04")
				data.Withdrawals = append(data.Withdrawals, wd)
			}
		}
	}
	a.renderAs(w, "admin_withdrawals", http.StatusOK, data)
}

func (a *App) handleAdminReviewWithdrawal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	withdrawalIDStr := chi.URLParam(r, "id")
	status := r.FormValue("status") // approved, paid, rejected
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil || (status != "approved" && status != "paid" && status != "rejected") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	wid, ok := parseUUID(withdrawalIDStr)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if ws, ok := a.vendor.(WithdrawalStore); ok {
		if status == "paid" {
			if wr, err := ws.GetWithdrawal(ctx, wid); err == nil && wr != nil {
				if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
					tx, serr := NewMoneroProvider().SendTransaction(ctx, wr.XmrAddress, wr.AmountPiconero)
					if serr != nil { w.WriteHeader(http.StatusBadGateway); return }
					_, _ = pool.Exec(ctx, `UPDATE withdrawals SET tx_hash=$2 WHERE id=$1`, wid, tx)
					var txref pgtype.UUID
					_ = pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&txref)
					_ = NewLedgerService(pool).RecordLedgerTx(ctx, txref, "withdraw:"+uuidString(wid), &wr.VendorID, "vendor withdrawal paid", []LedgerLeg{
						{AccountName: vendorAccountName(wr.VendorID), AmountPiconero: wr.AmountPiconero, Direction: "debit"},
						{AccountName: "escrow", AmountPiconero: wr.AmountPiconero, Direction: "credit"},
					})
				}
			}
		}
		if err := ws.ReviewWithdrawal(ctx, wid, *uid, status); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if a.auth != nil {
			_ = a.auth.AuditAppend(ctx, *uid, "user", "withdrawal."+status, "withdrawals:"+withdrawalIDStr, "{}")
		}
	}

	http.Redirect(w, r, "/admin/withdrawals", http.StatusSeeOther)
}

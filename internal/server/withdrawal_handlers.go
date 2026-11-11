package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// WithdrawalRecord represents a row from the withdrawals table.
type WithdrawalRecord struct {
	ID         pgtype.UUID
	VendorID   pgtype.UUID
	AmountSat  int64
	BtcAddress string
	Status     string
	CreatedAt  time.Time
}

// WithdrawalStore defines database operations for withdrawals and wallet balances.
type WithdrawalStore interface {
	VendorGetByUser(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error)
	GetVendorBalanceSat(ctx context.Context, vendorID pgtype.UUID) (int64, error)
	CreateWithdrawalRequest(ctx context.Context, vendorID pgtype.UUID, amountSat int64, btcAddress string) error
	ListWithdrawalsByVendor(ctx context.Context, vendorID pgtype.UUID) ([]WithdrawalRecord, error)
	ListPendingWithdrawals(ctx context.Context) ([]WithdrawalRecord, error)
	ReviewWithdrawal(ctx context.Context, withdrawalID pgtype.UUID, reviewerID pgtype.UUID, status string) error
	GetWithdrawal(ctx context.Context, withdrawalID pgtype.UUID) (*WithdrawalRecord, error)
}

// RegisterWithdrawalRoutes mounts wallet and withdrawal routes.
func (a *App) RegisterWithdrawalRoutes(r chi.Router, store WithdrawalStore) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	adminOnly := a.RequireRole("admin")

	r.Method(http.MethodGet, "/wallet", anyUser(http.HandlerFunc(a.handleWallet)))
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
	data := a.baseData(r)
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil {
		writeGenericError(w)
		return
	}

	fail := func(msg string) {
		data.Error = msg
		a.render(w, "wallet", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) {
		fail("submission rejected")
		return
	}

	if a.auth == nil {
		fail("service unavailable")
		return
	}

	v, err := a.auth.VendorGetByUser(ctx, *uid)
	if err != nil || v == nil || !v.Approved {
		fail("only approved vendors can request withdrawals")
		return
	}

	amountStr := strings.TrimSpace(r.FormValue("amount_sat"))
	btcAddress := strings.TrimSpace(r.FormValue("btc_address"))
	amountSat, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || amountSat <= 0 {
		fail("invalid withdrawal amount")
		return
	}

	if btcAddress == "" {
		fail("bitcoin address is required")
		return
	}

	// Create withdrawal request (store implementation handles balance checks)
	if ws, ok := a.vendor.(WithdrawalStore); ok {
		if err := ws.CreateWithdrawalRequest(ctx, v.ID, amountSat, btcAddress); err != nil {
			fail("could not create withdrawal request: " + err.Error())
			return
		}
	} else {
		fail("withdrawal service unavailable")
		return
	}

	data.Flash = "Withdrawal request submitted successfully (subject to review delay)."
	a.render(w, "wallet", http.StatusOK, data)
}

func (a *App) handleAdminWithdrawals(w http.ResponseWriter, r *http.Request) {
	// Admin view for pending withdrawals
	data := a.baseData(r)
	a.render(w, "admin_withdrawals", http.StatusOK, data)
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

package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---- store ----

func vendorFeeCents(ctx context.Context, pool *pgxpool.Pool) int64 {
	var v string
	_ = pool.QueryRow(ctx, `SELECT value FROM site_settings WHERE key='vendor_fee_usd_cents'`).Scan(&v)
	n, _ := strconv.ParseInt(v, 10, 64)
	if n < 0 {
		return 0
	}
	return n
}

func vendorFeePaidInfo(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) (bool, string) {
	var tx string
	err := pool.QueryRow(ctx, `SELECT COALESCE(tx_hash,'') FROM vendor_fee_payments
	                           WHERE vendor_id=$1 AND status='paid'`, vendorID).Scan(&tx)
	return err == nil, tx
}

type FeeView struct {
	AmountPico int64
	Address    string
	Status     string
	CreatedAt  string
}

func vendorFeeActive(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) *FeeView {
	var f FeeView
	var id pgtype.UUID
	var created interface{}
	err := pool.QueryRow(ctx, `SELECT id, amount_piconero, xmr_address, status, created_at
	                           FROM vendor_fee_payments
	                           WHERE vendor_id=$1 AND status='awaiting_payment'`, vendorID).
		Scan(&id, &f.AmountPico, &f.Address, &f.Status, &created)
	if err != nil {
		return nil
	}
	cs := fmt.Sprintf("%v", created)
	if len(cs) > 16 {
		cs = cs[:16]
	}
	f.CreatedAt = cs
	return &f
}

// vendorFeeSettled is the hard gate: fee disabled (0) or paid.
func vendorFeeSettled(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) bool {
	if vendorFeeCents(ctx, pool) <= 0 {
		return true
	}
	paid, _ := vendorFeePaidInfo(ctx, pool, vendorID)
	return paid
}

// ---- routes ----

func (a *App) registerVendorFeeRoutes(r chi.Router) {
	vendorOrAdmin := a.RequireRole("vendor", "admin")
	adminOnly := a.RequireRole("admin")
	r.Method(http.MethodGet, "/vendor/fee", vendorOrAdmin(http.HandlerFunc(a.handleVendorFeeGet)))
	r.Method(http.MethodPost, "/vendor/fee", vendorOrAdmin(http.HandlerFunc(a.handleVendorFeePost)))
	r.Method(http.MethodGet, "/admin/settings", adminOnly(http.HandlerFunc(a.handleAdminSettings)))
	r.Method(http.MethodPost, "/admin/settings", adminOnly(http.HandlerFunc(a.handleAdminSettingsPost)))
}

// ---- vendor side ----

type VendorFeeData struct {
	AuthData
	FeeCents int64
	FeeZero  bool
	Paid     bool
	TxHash   string
	Active   *FeeView
}

func (a *App) handleVendorFeeGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var vid pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM vendors WHERE user_id=$1`, uid).Scan(&vid); err != nil {
		http.Redirect(w, r, "/vendor/apply", http.StatusSeeOther)
		return
	}
	data := VendorFeeData{AuthData: a.baseData(r)}
	data.FeeCents = vendorFeeCents(ctx, pool)
	if data.FeeCents <= 0 {
		data.FeeZero = true
	} else if paid, tx := vendorFeePaidInfo(ctx, pool, vid); paid {
		data.Paid, data.TxHash = true, tx
	} else {
		data.Active = vendorFeeActive(ctx, pool, vid)
	}
	a.renderAs(w, "vendor_fee", http.StatusOK, data)
}

func (a *App) handleVendorFeePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) {
		http.Redirect(w, r, "/vendor/fee", http.StatusSeeOther)
		return
	}
	var vid pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM vendors WHERE user_id=$1`, uid).Scan(&vid); err != nil {
		http.Redirect(w, r, "/vendor/apply", http.StatusSeeOther)
		return
	}
	cents := vendorFeeCents(ctx, pool)
	if cents <= 0 {
		http.Redirect(w, r, "/vendor/fee", http.StatusSeeOther)
		return
	}
	if paid, _ := vendorFeePaidInfo(ctx, pool, vid); paid {
		http.Redirect(w, r, "/vendor/fee", http.StatusSeeOther)
		return
	}
	if vendorFeeActive(ctx, pool, vid) != nil {
		http.Redirect(w, r, "/vendor/fee", http.StatusSeeOther)
		return
	}
	oracle := ratesOracle
	if oracle == nil {
		oracle = NewExchangeRates()
	}
	pico := oracle.USDCentsToPiconero(cents)
	pico = ((pico + 500000) / 1000000) * 1000000
	var payID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&payID); err != nil {
		writeGenericError(w)
		return
	}
	addr, _, err := NewMoneroProvider().CreatePaymentAddress(ctx, payID)
	if err != nil || addr == "" {
		log.Printf("server: vendor fee address creation failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO vendor_fee_payments (vendor_id, amount_piconero, xmr_address)
		VALUES ($1,$2,$3)`, vid, pico, addr); err != nil {
		log.Printf("server: vendor fee row insert failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/vendor/fee", http.StatusSeeOther)
}

// ---- admin side ----

type ApplicantView struct {
	Username string
	ShopName string
	Approved bool
	FeeState string
}

type AdminSettingsData struct {
	AuthData
	FeeUSD     string
	Applicants []ApplicantView
	Flash      string
}

func (a *App) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		writeGenericError(w)
		return
	}
	cents := vendorFeeCents(ctx, pool)
	data := AdminSettingsData{
		AuthData: a.baseData(r),
		FeeUSD:   fmt.Sprintf("%d.%02d", cents/100, cents%100),
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Flash = "Vendor fee updated."
	}
	rows, err := pool.Query(ctx, `
		SELECT u.username, COALESCE(v.shop_name,''), v.approved,
		       COALESCE((SELECT p.status FROM vendor_fee_payments p
		                 WHERE p.vendor_id=v.id ORDER BY p.created_at DESC LIMIT 1),'none')
		FROM vendors v JOIN users u ON u.id=v.user_id
		ORDER BY v.created_at DESC`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var av ApplicantView
			if err := rows.Scan(&av.Username, &av.ShopName, &av.Approved, &av.FeeState); err == nil {
				data.Applicants = append(data.Applicants, av)
			}
		}
	}
	a.renderAs(w, "admin_settings", http.StatusOK, data)
}

func (a *App) handleAdminSettingsPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		writeGenericError(w)
		return
	}
	if honeypotTriggered(r) {
		http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
		return
	}
	cents, err := parseDecimalUnitsAllowZero(r.FormValue("fee_usd"), 2)
	if err != nil || cents > 10000000 {
		http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO site_settings(key, value, updated_at) VALUES ('vendor_fee_usd_cents',$1,now())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`,
		strconv.FormatInt(cents, 10)); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

// ---- fee poller ----

func StartVendorFeePoller(ctx context.Context, pool *pgxpool.Pool, provider MoneroProvider) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pollVendorFees(ctx, pool, provider)
		}
	}
}

func pollVendorFees(ctx context.Context, pool *pgxpool.Pool, provider MoneroProvider) {
	_, _ = pool.Exec(ctx, `UPDATE vendor_fee_payments SET status='expired'
	                       WHERE status='awaiting_payment' AND created_at < now() - interval '12 hours'`)
	rows, err := pool.Query(ctx, `SELECT id, amount_piconero, xmr_address FROM vendor_fee_payments
	                              WHERE status='awaiting_payment' AND created_at > now() - interval '12 hours'`)
	if err != nil {
		log.Printf("server: vendor fee query failed: %v", sanitizedErr(err))
		return
	}
	defer rows.Close()
	var pending []struct {
		id   pgtype.UUID
		pico int64
		addr string
	}
	for rows.Next() {
		var p struct {
			id   pgtype.UUID
			pico int64
			addr string
		}
		if err := rows.Scan(&p.id, &p.pico, &p.addr); err == nil {
			pending = append(pending, p)
		}
	}
	for _, p := range pending {
		paid, tx, err := provider.CheckPayment(ctx, p.id, p.addr, p.pico)
		if err != nil || !paid {
			continue
		}
		if _, err := pool.Exec(ctx, `UPDATE vendor_fee_payments
		                             SET status='paid', tx_hash=$1, paid_at=now()
		                             WHERE id=$2 AND status='awaiting_payment'`, tx, p.id); err != nil {
			log.Printf("server: vendor fee mark-paid failed: %v", sanitizedErr(err))
			continue
		}
		log.Printf("server: vendor fee paid for payment %s (tx %s)", uuidString(p.id), tx)
	}
}

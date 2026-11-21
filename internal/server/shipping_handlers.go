package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ShippingData struct {
	AuthData
	ShippingOptions []ShippingOption
	Error           string
}

func (a *App) registerShippingRoutes(r chi.Router) {
	vendorOrAdmin := a.RequireRole("vendor", "admin")
	r.Method(http.MethodGet, "/vendor/shipping", vendorOrAdmin(http.HandlerFunc(a.handleShippingList)))
	r.Method(http.MethodPost, "/vendor/shipping", vendorOrAdmin(http.HandlerFunc(a.handleShippingCreate)))
	r.Method(http.MethodPost, "/vendor/shipping/{id}/toggle", vendorOrAdmin(http.HandlerFunc(a.handleShippingToggle)))
	r.Method(http.MethodPost, "/vendor/shipping/{id}/delete", vendorOrAdmin(http.HandlerFunc(a.handleShippingDelete)))
}

func (a *App) shippingPool() (*pgxpool.Pool, bool) {
	p, ok := a.deps.Pool.(*pgxpool.Pool)
	return p, ok
}

func (a *App) handleShippingList(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.shippingPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	vrow, err := a.vendorRow(r.Context(), uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	opts, err := ShippingListByVendor(r.Context(), pool, vrow.ID)
	if err != nil {
		writeGenericError(w)
		return
	}
	data := ShippingData{AuthData: a.baseData(r), ShippingOptions: opts}
	// Single render point: layout.html shows .Error / .Flash. Do NOT render again here.
	switch r.URL.Query().Get("error") {
	case "active_in_use":
		data.Error = "Cannot delete: this method is active and referenced by existing orders. Disable it first, then it can be removed."
	case "not_found":
		data.Error = "Shipping option not found."
	}
	if r.URL.Query().Get("deleted") == "1" {
		data.Flash = "Shipping option deleted."
	}
	a.renderAs(w, "vendor_shipping", http.StatusOK, data)
}

func (a *App) handleShippingCreate(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.shippingPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	vrow, err := a.vendorRow(r.Context(), uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	eta := strings.TrimSpace(r.FormValue("eta"))
	if len(title) < 2 || len(title) > 80 {
		http.Redirect(w, r, "/vendor/shipping?error=not_found", http.StatusSeeOther)
		return
	}
	switch eta {
	case "fast", "medium", "slow":
	default:
		eta = "medium"
	}
	etaDays, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("eta_days")))
	if etaDays < 1 || etaDays > 60 {
		etaDays = 14
	}
	priceUSD, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("price_usd")), 64)
	if err != nil || priceUSD < 0 {
		http.Redirect(w, r, "/vendor/shipping?error=not_found", http.StatusSeeOther)
		return
	}
	if err := ShippingCreate(r.Context(), pool, ShippingOption{
		VendorID: vrow.ID, Title: title, Description: "",
		PriceUSDCents: int64(priceUSD * 100), Eta: eta, EtaDays: etaDays, Active: true,
	}); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
}

func (a *App) handleShippingToggle(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.shippingPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	vrow, err := a.vendorRow(r.Context(), uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = ShippingToggle(r.Context(), pool, id, vrow.ID)
	http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
}

// handleShippingDelete: unreferenced -> delete; referenced+disabled -> detach
// (SET NULL on historical orders; shipping_usd_cents already denormalized so
// totals/ledger are unaffected) then delete; referenced+active -> block.
func (a *App) handleShippingDelete(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.shippingPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	vrow, err := a.vendorRow(r.Context(), uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var active bool
	if err := pool.QueryRow(r.Context(), `SELECT active FROM shipping_options WHERE id=$1 AND vendor_id=$2`, id, vrow.ID).Scan(&active); err != nil {
		http.Redirect(w, r, "/vendor/shipping?error=not_found", http.StatusSeeOther)
		return
	}
	var usage int
	_ = pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM orders WHERE shipping_option_id=$1`, id).Scan(&usage)
	if usage > 0 {
		if active {
			http.Redirect(w, r, "/vendor/shipping?error=active_in_use", http.StatusSeeOther)
			return
		}
		if _, err := pool.Exec(r.Context(), `UPDATE orders SET shipping_option_id=NULL WHERE shipping_option_id=$1`, id); err != nil {
			writeGenericError(w)
			return
		}
	}
	if err := ShippingDelete(r.Context(), pool, id, vrow.ID); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/vendor/shipping?deleted=1", http.StatusSeeOther)
}

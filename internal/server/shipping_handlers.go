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
	a.renderAs(w, "vendor_shipping", http.StatusOK, ShippingData{AuthData: a.baseData(r), ShippingOptions: opts})
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
	desc := strings.TrimSpace(r.FormValue("description"))
	eta := strings.TrimSpace(r.FormValue("eta"))
	if len(title) < 2 || len(title) > 80 {
		http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
		return
	}
	if len(desc) > 500 {
		desc = desc[:500]
	}
	switch eta {
	case "fast", "medium", "slow":
	default:
		eta = "medium"
	}
	priceUSD, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("price_usd")), 64)
	if err != nil || priceUSD < 0 {
		http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
		return
	}
	err = ShippingCreate(r.Context(), pool, ShippingOption{
		VendorID: vrow.ID, Title: title, Description: desc,
		PriceUSDCents: int64(priceUSD * 100), Eta: eta, Active: true,
	})
	if err != nil {
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
	_ = ShippingDelete(r.Context(), pool, id, vrow.ID)
	http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
}

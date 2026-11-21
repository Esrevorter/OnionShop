package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type VendorStatsData struct {
	AuthData
	Stats *VendorStats
}

func (a *App) registerVendorStatsRoutes(r chi.Router) {
	vendorOrAdmin := a.RequireRole("vendor", "admin")
	r.Method(http.MethodGet, "/vendor/stats", vendorOrAdmin(http.HandlerFunc(a.handleVendorStats)))
}

func (a *App) handleVendorStats(w http.ResponseWriter, r *http.Request) {
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
	pool, ok := a.msgPool()
	if !ok {
		writeGenericError(w)
		return
	}
	stats, err := ComputeVendorStats(r.Context(), pool, vrow.ID)
	if err != nil {
		writeGenericError(w)
		return
	}
	a.renderAs(w, "vendor_stats", http.StatusOK, VendorStatsData{AuthData: a.baseData(r), Stats: stats})
}

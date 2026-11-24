package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerHelpRoutes mounts the public Help Center. The operator guide is
// role-gated: operational detail is not public documentation.
func (a *App) registerHelpRoutes(r chi.Router) {
	r.Get("/help", a.handleHelpIndex)
	r.Get("/help/buyers", a.handleHelpBuyers)
	r.Get("/help/vendors", a.handleHelpVendors)
	adminOnly := a.RequireRole("admin")
	r.Method(http.MethodGet, "/help/admins", adminOnly(http.HandlerFunc(a.handleHelpAdmins)))
}

func (a *App) handleHelpIndex(w http.ResponseWriter, r *http.Request) {
	a.renderAs(w, "help", http.StatusOK, a.baseData(r))
}
func (a *App) handleHelpBuyers(w http.ResponseWriter, r *http.Request) {
	a.renderAs(w, "help_buyers", http.StatusOK, a.baseData(r))
}
func (a *App) handleHelpVendors(w http.ResponseWriter, r *http.Request) {
	a.renderAs(w, "help_vendors", http.StatusOK, a.baseData(r))
}
func (a *App) handleHelpAdmins(w http.ResponseWriter, r *http.Request) {
	a.renderAs(w, "help_admins", http.StatusOK, a.baseData(r))
}

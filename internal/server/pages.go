package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerPageRoutes mounts the UI pages for Phases 5-8 that were missing routes.
func (a *App) registerPageRoutes(r chi.Router) {
	// Middleware to require authentication
	requireLogin := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if CurrentUserID(r.Context(), a.sm) == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next(w, r)
		}
	}

	// Helper to mount a simple GET page that just renders a template
	mountPage := func(path, templateName string) {
		r.Get(path, requireLogin(func(w http.ResponseWriter, r *http.Request) {
			data := a.baseData(r)
			a.render(w, templateName, http.StatusOK, data)
		}))
	}

	// GET routes for the new pages
	mountPage("/wallet", "wallet")
	mountPage("/orders", "orders")
	mountPage("/disputes", "disputes")
	mountPage("/tickets", "tickets")
	mountPage("/withdraw", "withdraw")
	mountPage("/checkout", "checkout")
	mountPage("/vendor/products", "vendor_products")

	// POST routes for forms (placeholders for now - just redirect back)
	// In later phases, these will process the actual form data.
	handlePost := func(w http.ResponseWriter, r *http.Request) {
		// For now, just redirect back to the referring page
		ref := r.Referer()
		if ref == "" {
			ref = "/"
		}
		http.Redirect(w, r, ref, http.StatusSeeOther)
	}

	r.Post("/tickets", requireLogin(handlePost))
	r.Post("/withdraw", requireLogin(handlePost))
	r.Post("/checkout", requireLogin(handlePost))
	r.Post("/vendor/products", requireLogin(handlePost))
}

package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *App) registerPageRoutes(r chi.Router) {
	requireLogin := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if CurrentUserID(r.Context(), a.sm) == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next(w, r)
		}
	}

	mountPage := func(path, templateName string) {
		r.Get(path, requireLogin(func(w http.ResponseWriter, r *http.Request) {
			data := a.baseData(r)
			a.render(w, templateName, http.StatusOK, data)
		}))
	}

	mountPage("/wallet", "wallet")
	r.Get("/orders", a.renderOrdersPage)
	mountPage("/disputes", "disputes")
	mountPage("/tickets", "tickets")
	mountPage("/withdraw", "withdraw")
	mountPage("/checkout", "checkout")
	mountPage("/vendor/products", "vendor_products")

	// Placeholder POST handlers that ENFORCE friction to establish the secure pattern.
	// When real logic is added, it simply replaces the redirect with actual processing.
	handleSecurePost := func(purpose string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !a.requireFriction(w, r, purpose) {
				// On friction failure, redirect back to the referring page.
				// The page will mint a fresh CAPTCHA on the next GET.
				ref := r.Referer()
				if ref == "" {
					ref = "/"
				}
				http.Redirect(w, r, ref, http.StatusSeeOther)
				return
			}
			ref := r.Referer()
			if ref == "" {
				ref = "/"
			}
			http.Redirect(w, r, ref, http.StatusSeeOther)
		}
	}

	r.Post("/tickets", requireLogin(handleSecurePost("ticket")))
	r.Post("/withdraw", requireLogin(handleSecurePost("withdraw")))
	r.Post("/checkout", requireLogin(handleSecurePost("checkout")))
	r.Post("/vendor/products", requireLogin(handleSecurePost("vendor")))
}

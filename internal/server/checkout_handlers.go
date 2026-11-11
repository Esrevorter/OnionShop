package server

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// CheckoutData represents the checkout page payload.
type CheckoutData struct {
	AuthData
	Cart        Cart
	TotalSat    int64
	InvoiceLink string
	InvoiceID   string
}

func (a *App) registerCheckoutRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/checkout", anyUser(http.HandlerFunc(a.handleCheckoutGet)))
	r.Method(http.MethodPost, "/checkout", anyUser(http.HandlerFunc(a.handleCheckoutPost)))
}

func (a *App) handleCheckoutGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	cart, err := GetCart(ctx, a.deps.RDB, uid)
	if err != nil {
		log.Printf("server: get cart failed: %v", err)
		writeGenericError(w)
		return
	}
	var total int64
	for _, item := range cart {
		total += item.PriceSat * int64(item.Qty)
	}
	data := CheckoutData{
		AuthData: a.baseData(r),
		Cart:     cart,
		TotalSat: total,
	}
	a.renderCheckout(w, "checkout", http.StatusOK, data)
}

func (a *App) handleCheckoutPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	cart, err := GetCart(ctx, a.deps.RDB, uid)
	if err != nil || len(cart) == 0 {
		http.Redirect(w, r, "/checkout", http.StatusSeeOther)
		return
	}
	data := CheckoutData{
		AuthData:    a.baseData(r),
		Cart:        cart,
		InvoiceLink: "https://btcpay.example.com/invoice?id=test",
		InvoiceID:   "test-invoice-id",
	}
	a.renderCheckout(w, "checkout", http.StatusOK, data)
}

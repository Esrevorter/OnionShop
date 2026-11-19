package server

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// CartData represents the cart page payload.
type CartData struct {
	AuthData
	Cart     Cart
	TotalSat int64
}

// CheckoutData represents the checkout page payload.
type CheckoutData struct {
	AuthData
	Cart                 Cart
	TotalSat             int64
	DeliveryInstructions string
	OrderPlaced          bool
}

func (a *App) registerCheckoutRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/cart", anyUser(http.HandlerFunc(a.handleCartGet)))
	r.Method(http.MethodPost, "/cart/action", anyUser(http.HandlerFunc(a.handleCartAction)))
	r.Method(http.MethodGet, "/checkout", anyUser(http.HandlerFunc(a.handleCheckoutGet)))
	r.Method(http.MethodPost, "/checkout", anyUser(http.HandlerFunc(a.handleCheckoutSubmit)))
}

func (a *App) handleCartGet(w http.ResponseWriter, r *http.Request) {
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

	data := CartData{
		AuthData: a.baseData(r),
		Cart:     cart,
		TotalSat: total,
	}
	// Use renderAs which accepts 'any' and is already proven to work
	a.renderAs(w, "cart", http.StatusOK, data)
}

func (a *App) handleCartAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	
	if honeypotTriggered(r) || !a.requireFriction(w, r, "cart") {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}

	action := r.FormValue("action")
	productID := r.FormValue("product_id")

	cart, err := GetCart(ctx, a.deps.RDB, uid)
	if err != nil {
		log.Printf("server: get cart for action failed: %v", err)
		writeGenericError(w)
		return
	}

	if action == "remove" {
		delete(cart, productID)
	} else if action == "update" {
		qtyStr := r.FormValue("qty")
		qty, err := strconv.ParseInt(qtyStr, 10, 64)
		if err != nil || qty <= 0 {
			delete(cart, productID)
		} else {
			if item, ok := cart[productID]; ok {
				item.Qty = qty
				cart[productID] = item
			}
		}
	}

	if err := SaveCart(ctx, a.deps.RDB, uid, cart); err != nil {
		log.Printf("server: save cart failed: %v", err)
	}

	http.Redirect(w, r, "/cart", http.StatusSeeOther)
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

	if len(cart) == 0 {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
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
	
	if data.Captcha = a.newChallenge(w, r, "checkout"); data.Captcha == nil {
		return
	}
	// renderCheckout is already defined in render.go, so we use it here safely
	a.renderCheckout(w, "checkout", http.StatusOK, data)
}

func (a *App) handleCheckoutSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	
	data := CheckoutData{AuthData: a.baseData(r)}
	
	fail := func(msg string) {
		data.Error = msg
		cart, _ := GetCart(ctx, a.deps.RDB, uid)
		data.Cart = cart
		var total int64
		for _, item := range cart {
			total += item.PriceSat * int64(item.Qty)
		}
		data.TotalSat = total
		if data.Captcha = a.newChallenge(w, r, "checkout"); data.Captcha == nil {
			return
		}
		a.renderCheckout(w, "checkout", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) || !a.requireFriction(w, r, "checkout") {
		fail("Human verification failed, please try again.")
		return
	}

	cart, err := GetCart(ctx, a.deps.RDB, uid)
	if err != nil || len(cart) == 0 {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}

	delivery := strings.TrimSpace(r.FormValue("delivery_instructions"))
	if len(delivery) > 2000 {
		fail("Delivery instructions are too long (max 2000 characters).")
		return
	}

	log.Printf("server: order placed by user %s, delivery: %s", uid, delivery)
	
	// Clear the cart upon successful "order" placement
	emptyCart := make(Cart)
	if err := SaveCart(ctx, a.deps.RDB, uid, emptyCart); err != nil {
		log.Printf("server: clear cart after checkout failed: %v", err)
	}

	data.OrderPlaced = true
	data.Cart = emptyCart
	data.TotalSat = 0
	
	if data.Captcha = a.newChallenge(w, r, "checkout"); data.Captcha == nil {
		return
	}
	a.renderCheckout(w, "checkout", http.StatusOK, data)
}

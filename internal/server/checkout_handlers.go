package server

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CartData represents the cart page payload.
type CartData struct {
	AuthData
	Cart     Cart
	TotalUSDCents int64
}

// CheckoutData represents the checkout page payload.
type CheckoutData struct {
	AuthData
	Cart                 Cart
	TotalUSDCents             int64
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
		total += item.PriceUSDCents * int64(item.Qty)
	}

	data := CartData{
		AuthData: a.baseData(r),
		Cart:     cart,
		TotalUSDCents: total,
	}
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
		total += item.PriceUSDCents * int64(item.Qty)
	}

	data := CheckoutData{
		AuthData: a.baseData(r),
		Cart:     cart,
		TotalUSDCents: total,
	}
	
	if data.Captcha = a.newChallenge(w, r, "checkout"); data.Captcha == nil {
		return
	}
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
			total += item.PriceUSDCents * int64(item.Qty)
		}
		data.TotalUSDCents = total
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

	// 1. Type assert the interface to *pgxpool.Pool for transaction support
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		log.Printf("server: database pool type assertion failed")
		fail("Internal server error: database pool unavailable.")
		return
	}

	// 2. Split cart into vendor-scoped orders
	customerUUID, ok := parseUUID(uid)
	if !ok {
		fail("Invalid user session.")
		return
	}

	checkoutRef, orderIDs, err := SplitCartIntoVendorOrders(ctx, pool, customerUUID, cart)
	if err != nil {
		log.Printf("server: order splitting failed: %v", sanitizedErr(err))
		fail("Failed to create orders. Please try again.")
		return
	}

	// 3. Calculate XMR settlement amount (locked at checkout)
	rates := NewExchangeRates() // In prod, inject this into App struct
	xmrAmount := rates.USDCentsToPiconero(data.TotalUSDCents)

	// 4. Generate XMR payment addresses for each order
	provider := &MockMoneroProvider{}

	for _, orderID := range orderIDs {
		addr, pid, err := provider.CreatePaymentAddress(ctx, orderID, 0)
		if err != nil {
			log.Printf("server: failed to create XMR address for order %s: %v", uuidString(orderID), err)
			fail("Failed to generate payment address.")
			return
		}

		// Update the order with the XMR payment details
		_, err = pool.Exec(ctx, `
			UPDATE orders 
			SET xmr_address = $1, xmr_payment_id = $2, xmr_amount_piconero = $3, status = 'awaiting_payment'
			WHERE id = $3
		`, addr, pid, xmrAmount, orderID)
		if err != nil {
			log.Printf("server: failed to update order %s with XMR details: %v", uuidString(orderID), err)
			fail("Failed to save payment details.")
			return
		}
	}

	// 4. Clear the cart upon successful order creation
	emptyCart := make(Cart)
	if err := SaveCart(ctx, a.deps.RDB, uid, emptyCart); err != nil {
		log.Printf("server: clear cart after checkout failed: %v", sanitizedErr(err))
	}

	// 5. Redirect to the orders page so the user can see their pending payments
	http.Redirect(w, r, "/orders?ref="+uuidString(checkoutRef), http.StatusSeeOther)
}

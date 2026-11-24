package server

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CartData represents the cart page payload.
type CartData struct {
	AuthData
	Cart         Cart
	TotalSat     int64
	VendorGroups []CheckoutVendorGroup
}

// CheckoutShippingOption is a display-safe view of a vendor shipping option.
type CheckoutShippingOption struct {
	IDStr         string
	Title         string
	Eta           string
	PriceUSDCents int64
}

// CheckoutVendorGroup groups one vendor's cart items plus its shipping choices.
type CheckoutVendorGroup struct {
	VendorID   string
	ShopName   string
	ShopHandle string
	Items      []CartItem
	ItemsTotal int64
	Options    []CheckoutShippingOption
}

// ShippingChoice is the validated per-vendor selection made at checkout.
type ShippingChoice struct {
	OptionID pgtype.UUID
	Cents    int64
}

// CheckoutData represents the checkout page payload.
type CheckoutData struct {
	AuthData
	Cart                 Cart
	TotalSat             int64
	VendorGroups         []CheckoutVendorGroup
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
	groupsMap := map[string]*CheckoutVendorGroup{}
	for _, item := range cart {
		line := item.PriceUSDCents * int64(item.Qty)
		total += line
		g, exists := groupsMap[item.VendorID]
		if !exists {
			g = &CheckoutVendorGroup{VendorID: item.VendorID}
			groupsMap[item.VendorID] = g
		}
		g.Items = append(g.Items, item)
		g.ItemsTotal += line
	}
	groups := make([]CheckoutVendorGroup, 0, len(groupsMap))
	for _, g := range groupsMap {
		if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
			if vuid, ok := parseUUID(g.VendorID); ok {
				if opts, err := ShippingListActiveByVendor(ctx, pool, vuid); err == nil {
					for _, o := range opts {
						g.Options = append(g.Options, CheckoutShippingOption{
							IDStr: uuidString(o.ID), Title: o.Title, Eta: o.Eta, PriceUSDCents: o.PriceUSDCents,
						})
					}
				}
			}
		}
		groups = append(groups, *g)
	}
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		for i := range groups {
			if vuid, ok := parseUUID(groups[i].VendorID); ok {
				groups[i].ShopName, groups[i].ShopHandle = vendorShopInfo(ctx, pool, vuid)
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].VendorID < groups[j].VendorID })
	a.renderAs(w, "cart", http.StatusOK, CartData{AuthData: a.baseData(r), Cart: cart, TotalSat: total, VendorGroups: groups})
}

func (a *App) handleCartAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	if honeypotTriggered(r) {
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
		qty, err := strconv.Atoi(qtyStr)
		if err != nil || qty <= 0 {
			delete(cart, productID)
		} else if item, ok := cart[productID]; ok {
			item.Qty = int64(qty)
			cart[productID] = item
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
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		writeGenericError(w)
		return
	}
	var total int64
	groupsMap := map[string]*CheckoutVendorGroup{}
	for _, item := range cart {
		line := item.PriceUSDCents * int64(item.Qty)
		total += line
		g, exists := groupsMap[item.VendorID]
		if !exists {
			g = &CheckoutVendorGroup{VendorID: item.VendorID}
			groupsMap[item.VendorID] = g
		}
		g.Items = append(g.Items, item)
		g.ItemsTotal += line
	}
	groups := make([]CheckoutVendorGroup, 0, len(groupsMap))
	for _, g := range groupsMap {
		if vuid, ok := parseUUID(g.VendorID); ok {
			if opts, err := ShippingListActiveByVendor(ctx, pool, vuid); err == nil {
				for _, o := range opts {
					g.Options = append(g.Options, CheckoutShippingOption{
						IDStr: uuidString(o.ID), Title: o.Title, Eta: o.Eta, PriceUSDCents: o.PriceUSDCents,
					})
				}
			}
		}
		groups = append(groups, *g)
	}
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		for i := range groups {
			if vuid, ok := parseUUID(groups[i].VendorID); ok {
				groups[i].ShopName, groups[i].ShopHandle = vendorShopInfo(ctx, pool, vuid)
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].VendorID < groups[j].VendorID })

	data := CheckoutData{AuthData: a.baseData(r), Cart: cart, TotalSat: total, VendorGroups: groups}
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
	refundAddr := strings.TrimSpace(r.FormValue("refund_address"))
	if len(refundAddr) < 10 {
		fail("Please provide a refund XMR address (used if the order is refunded).")
		return
	}
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok {
		fail("Internal server error: database pool unavailable.")
		return
	}
	customerUUID, ok := parseUUID(uid)
	if !ok {
		fail("Invalid user session.")
		return
	}

	// Validate one shipping selection per vendor that offers options.
	vendorSet := map[string]bool{}
	for _, item := range cart {
		vendorSet[item.VendorID] = true
	}
	selected := map[string]ShippingChoice{}
	for vid := range vendorSet {
		vuid, ok := parseUUID(vid)
		if !ok {
			continue
		}
		opts, err := ShippingListActiveByVendor(ctx, pool, vuid)
		if err != nil {
			fail("Failed to load shipping options.")
			return
		}
		if len(opts) == 0 {
			fail("A vendor in your cart no longer has active shipping options. Please remove their items.")
			return
		}
		selStr := strings.TrimSpace(r.FormValue("shipping_" + vid))
		if selStr == "" {
			fail("Please choose a shipping method for every vendor.")
			return
		}
		selID, ok := parseUUID(selStr)
		if !ok {
			fail("Invalid shipping selection.")
			return
		}
		found := false
		for _, o := range opts {
			if uuidString(o.ID) == uuidString(selID) {
				selected[vid] = ShippingChoice{OptionID: o.ID, Cents: o.PriceUSDCents}
				found = true
			}
		}
		if !found {
			fail("Invalid shipping selection.")
			return
		}
	}

	checkoutRef, orderIDs, err := SplitCartIntoVendorOrders(ctx, pool, customerUUID, cart)
	if err != nil {
		log.Printf("server: order splitting failed: %v", sanitizedErr(err))
		fail("Failed to create orders. Please try again.")
		return
	}

	oracle := ratesOracle
	if oracle == nil {
		oracle = NewExchangeRates()
	}
	provider := NewMoneroProvider()

	for _, orderID := range orderIDs {
		var vendorUUID pgtype.UUID
		var orderItemsTotal int64
		if err := pool.QueryRow(ctx, `SELECT vendor_id, total_usd_cents FROM orders WHERE id=$1`, orderID).
			Scan(&vendorUUID, &orderItemsTotal); err != nil {
			log.Printf("server: order total lookup failed: %v", sanitizedErr(err))
			fail("Failed to finalize order totals.")
			return
		}
		choice := selected[uuidString(vendorUUID)]
		newTotal := orderItemsTotal + choice.Cents
		xmrAmount := oracle.USDCentsToPiconero(newTotal)

		addr, pid, err := provider.CreatePaymentAddress(ctx, orderID)
		if err != nil {
			log.Printf("server: failed to create XMR address for order %s: %v", uuidString(orderID), err)
			fail("Failed to generate payment address.")
			return
		}
		if _, err := pool.Exec(ctx, `
			UPDATE orders
			SET total_usd_cents=$1, shipping_option_id=$2, shipping_usd_cents=$3,
			    xmr_address=$4, xmr_payment_id=$5, xmr_amount_piconero=$6, refund_address=$8, status='awaiting_payment'
			WHERE id=$7
		`, newTotal, choice.OptionID, choice.Cents, addr, pid, xmrAmount, orderID, refundAddr); err != nil {
			log.Printf("server: failed to update order %s: %v", uuidString(orderID), err)
			fail("Failed to save payment details.")
			return
		}
		if err := notifyOrder(ctx, pool, orderID, "created"); err != nil {
			log.Printf("server: order notify(created) failed: %v", sanitizedErr(err))
		}
	}

	emptyCart := make(Cart)
	if err := SaveCart(ctx, a.deps.RDB, uid, emptyCart); err != nil {
		log.Printf("server: clear cart after checkout failed: %v", sanitizedErr(err))
	}
	http.Redirect(w, r, "/orders?ref="+uuidString(checkoutRef), http.StatusSeeOther)
}

// vendorShopInfo resolves a vendor's display name and shop handle for UI.
// Degrades gracefully: join fails -> shop_name only; that fails -> empty and
// the template falls back to the raw vendor id.
func vendorShopInfo(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) (string, string) {
	var name, handle string
	err := pool.QueryRow(ctx, `
		SELECT v.shop_name, COALESCE(u.username, '')
		FROM vendors v LEFT JOIN users u ON u.id = v.user_id
		WHERE v.id = $1`, vendorID).Scan(&name, &handle)
	if err == nil {
		return name, handle
	}
	if err2 := pool.QueryRow(ctx, `SELECT shop_name FROM vendors WHERE id=$1`, vendorID).Scan(&name); err2 == nil {
		return name, ""
	}
	return "", ""
}

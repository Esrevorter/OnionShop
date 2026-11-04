package server

// Phase 4 vendor product management (TASKS.md "Catalog & Vendors"): the
// /vendor/products dashboard plus create/edit/remove routes. The store layer
// (catalog_store.go) already enforces ownership in SQL (every write carries a
// vendor_id predicate), so handlers never trust form-supplied ids beyond the
// parse-and-reject pattern used across the auth surface. Every POST rides the
// same gate/friction stack: RequireRole("vendor") + CSRF (global middleware)
// + image CAPTCHA + honeypot, and every listing change lands in audit_log
// with a stable verb (product.create/update/remove, SECURITY §3).

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// productPurpose is the CAPTCHA slot for all three product forms. One
	// slot per surface keeps replay of a solved challenge off the other
	// forms (captcha.go binds purpose into the HMAC).
	productPurpose = "product"

	// Mirrors the products table CHECK constraints (migration 0001):
	// title 3..200 chars, description <= 20000, price_sat > 0, stock >= 0.
	productTitleMin, productTitleMax                              = 3, 200
	productDescMax                                                = 20000
	productStatusDraft, productStatusActive, productStatusRemoved = "draft", "active", "removed"
)

// ProductFormData extends AuthData with the vendor dashboard payload. It
// embeds AuthData so baseData stays the one place CSRF/phrase/flash/error
// are assembled; Products/Categories feed the list + the category dropdown.
type ProductFormData struct {
	AuthData
	Products   []ProductRecord
	Categories []CategoryRecord
	Editing    *ProductRecord // non-nil on the edit form (prefill)
	ShopName   string         // this account's shop, shown as page context
}

// productData builds the typed payload for the vendor product templates.
func (a *App) productData(r *http.Request) *ProductFormData {
	return &ProductFormData{AuthData: a.baseData(r)}
}

// vendorRow resolves the CALLER's own approved vendor row. Handlers pass the
// session user id only — never a form value — so a vendor can never act on
// someone else's storefront even if the store predicate were missing.
func (a *App) vendorRow(ctx context.Context, userID pgtype.UUID) (*VendorRecord, error) {
	v, err := a.auth.VendorGetByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if v == nil || !v.Approved {
		return nil, ErrNotFound
	}
	return v, nil
}

// registerProductRoutes mounts the vendor product CRUD surface. Called from
// registerVendorRoutes once both stores exist; role gate is exactly 'vendor'
// (mods/admins manage through the admin queue, not listings).
func (a *App) registerProductRoutes(r chi.Router) {
	vendorOnly := a.RequireRole("vendor")
	r.Method(http.MethodGet, "/vendor/products", vendorOnly(http.HandlerFunc(a.handleProductList)))
	r.Method(http.MethodGet, "/vendor/products/new", vendorOnly(http.HandlerFunc(a.handleProductNew)))
	r.Method(http.MethodPost, "/vendor/products/new", vendorOnly(http.HandlerFunc(a.handleProductCreate)))
	r.Method(http.MethodGet, "/vendor/products/{id}/edit", vendorOnly(http.HandlerFunc(a.handleProductEdit)))
	r.Method(http.MethodPost, "/vendor/products/{id}/edit", vendorOnly(http.HandlerFunc(a.handleProductUpdate)))
	r.Method(http.MethodPost, "/vendor/products/{id}/remove", vendorOnly(http.HandlerFunc(a.handleProductRemove)))
}

// handleProductList shows every listing owned by the caller (drafts included
// — the owner sees their whole shelf; the public store filters status).
func (a *App) handleProductList(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	data := a.productData(r)
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	vrow, err := a.vendorRow(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			data.Error = "no approved vendor profile on this account"
			a.renderAs(w, "vendor_products", http.StatusOK, data)
			return
		}
		log.Printf("server: vendor row lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data.ShopName = vrow.ShopName
	products, err := a.vendor.CatalogListVendorProducts(ctx, uid)
	if err != nil {
		log.Printf("server: vendor product list failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	cats, err := a.vendor.CatalogListCategories(ctx)
	if err != nil {
		// Dropdown without categories still renders a usable dashboard.
		log.Printf("server: category list failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Products, data.Categories = products, cats
	a.renderAs(w, "vendor_products", http.StatusOK, data)
}

// parseProductForm reads + validates one product form into a ProductRecord
// scoped to the caller's vendor row. Returns (record, "") on success or
// (nil, userMsg) with a generic verb — never echoing rejected input back.
func (a *App) parseProductForm(r *http.Request, vendorID pgtype.UUID) (*ProductRecord, string) {
	title := strings.TrimSpace(r.FormValue("title"))
	if len(title) < productTitleMin || len(title) > productTitleMax {
		return nil, "title must be 3-200 characters"
	}
	desc := strings.TrimSpace(r.FormValue("description"))
	if len(desc) > productDescMax {
		return nil, "description too long"
	}
	price, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("price_sat")), 10, 64)
	if err != nil || price <= 0 {
		return nil, "price must be a positive number of satoshis"
	}
	stock, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("stock")), 10, 64)
	if err != nil || stock < 0 {
		return nil, "stock must be zero or more"
	}
	var catID int64
	if s := strings.TrimSpace(r.FormValue("category_id")); s != "" {
		catID, err = strconv.ParseInt(s, 10, 64)
		if err != nil || catID < 0 {
			return nil, "unknown category"
		}
	}
	status := strings.TrimSpace(r.FormValue("status"))
	switch status {
	case productStatusDraft, productStatusActive:
	case "":
		status = productStatusDraft
	default:
		return nil, "unknown listing state"
	}
	return &ProductRecord{
		VendorID:    vendorID,
		CategoryID:  catID,
		Title:       title,
		Description: desc,
		PriceSat:    price,
		Stock:       stock,
		Status:      status,
	}, ""
}

func (a *App) handleProductNew(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
	}
	data := a.productData(r)
	cats, err := a.vendor.CatalogListCategories(r.Context())
	if err != nil {
		log.Printf("server: category list failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Categories = cats
	if data.Captcha = a.newChallenge(w, r, productPurpose); data.Captcha == nil {
		return
	}
	a.renderAs(w, "vendor_product_edit", http.StatusOK, data)
}

func (a *App) handleProductCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.productData(r)
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, productPurpose); data.Captcha == nil {
			return
		}
		cats, err := a.vendor.CatalogListCategories(ctx)
		if err == nil {
			data.Categories = cats
		}
		a.renderAs(w, "vendor_product_edit", http.StatusBadRequest, data)
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, productPurpose) {
		fail("challenge failed, please try again")
		return
	}
	vrow, err := a.vendorRow(ctx, uid)
	if err != nil {
		fail("no approved vendor profile on this account")
		return
	}
	p, msg := a.parseProductForm(r, vrow.ID)
	if msg != "" {
		fail(msg)
		return
	}
	created, err := a.vendor.CatalogCreateProduct(ctx, *p)
	if err != nil {
		log.Printf("server: product create failed: %v", sanitizedErr(err))
		fail("listing failed, please try again")
		return
	}
	a.auditProduct(ctx, uid, actionProductCreate, created)
	data.Flash = "Listing saved."
	a.renderAs(w, "vendor_product_edit", http.StatusOK, data)
}

// loadEditableProduct re-reads the target row THROUGH the owner-scoped list
// (never trusting the form): the {id} must appear among the caller's own
// listings or it is treated as absent. This closes both IDOR and the
// "edited row vanished between GET and POST" race.
func (a *App) loadEditableProduct(ctx context.Context, vendorUserID, productID pgtype.UUID) (*ProductRecord, error) {
	products, err := a.vendor.CatalogListVendorProducts(ctx, vendorUserID)
	if err != nil {
		return nil, err
	}
	for i := range products {
		if uuidString(products[i].ID) == uuidString(productID) {
			return &products[i], nil
		}
	}
	return nil, ErrNotFound
}

func (a *App) handleProductEdit(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	data := a.productData(r)
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	pid, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	existing, err := a.loadEditableProduct(ctx, uid, pid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("server: product edit lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if existing.Status == productStatusRemoved {
		http.NotFound(w, r) // removed listings are read-only history
		return
	}
	cats, err := a.vendor.CatalogListCategories(ctx)
	if err != nil {
		log.Printf("server: category list failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Categories, data.Editing = cats, existing
	if data.Captcha = a.newChallenge(w, r, productPurpose); data.Captcha == nil {
		return
	}
	a.renderAs(w, "vendor_product_edit", http.StatusOK, data)
}

func (a *App) handleProductUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.productData(r)
	fail := func(code int, msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, productPurpose); data.Captcha == nil {
			return
		}
		a.renderAs(w, "vendor_product_edit", code, data)
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, productPurpose) {
		fail(http.StatusUnauthorized, "challenge failed, please try again")
		return
	}
	pid, err := parseUUIDParam(r, "id")
	if err != nil {
		fail(http.StatusBadRequest, "unknown listing")
		return
	}
	existing, err := a.loadEditableProduct(ctx, uid, pid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			fail(http.StatusBadRequest, "unknown listing")
			return
		}
		log.Printf("server: product update lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if existing.Status == productStatusRemoved {
		fail(http.StatusBadRequest, "unknown listing")
		return
	}
	vrow, err := a.vendorRow(ctx, uid)
	if err != nil {
		fail(http.StatusBadRequest, "no approved vendor profile on this account")
		return
	}
	p, msg := a.parseProductForm(r, vrow.ID)
	if msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}
	p.ID = existing.ID
	// ImagePath is NOT form-controlled: scrubbed upload paths arrive via the
	// Phase 5 media pipeline; updates preserve whatever the row already has.
	p.ImagePath = existing.ImagePath
	updated, err := a.vendor.CatalogUpdateProduct(ctx, *p)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			fail(http.StatusBadRequest, "unknown listing")
			return
		}
		log.Printf("server: product update failed: %v", sanitizedErr(err))
		fail(http.StatusInternalServerError, "update failed, please try again")
		return
	}
	a.auditProduct(ctx, uid, actionProductUpdate, updated)
	data.Flash = "Listing updated."
	a.renderAs(w, "vendor_product_edit", http.StatusOK, data)
}

func (a *App) handleProductRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.productData(r)
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Remove is a plain POST behind role+CSRF; the friction puzzle guards the
	// destructive path like every other state change here.
	if honeypotTriggered(r) || !a.requireFriction(w, r, productPurpose) {
		a.handleProductList(w, r) // re-render dashboard, no internals echoed
		return
	}
	pid, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.vendor.CatalogRemoveProduct(ctx, pid, uid); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("server: product remove failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	details, _ := json.Marshal(map[string]any{"product_id": uuidString(pid)})
	if err := a.auth.AuditAppend(ctx, uid, "user", actionProductRemove, "product:"+uuidString(pid), string(details)); err != nil {
		log.Printf("server: product remove audit write failed (non-fatal): %v", sanitizedErr(err))
	}
	// Reload the shelf so the removed row disappears immediately; the flash
	// rides into handleProductList's payload (same typed shape, no redirect
	// round-trip — every state change here re-renders in place).
	vrow, err := a.vendorRow(ctx, uid)
	if err != nil {
		log.Printf("server: product remove post-render lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	products, err := a.vendor.CatalogListVendorProducts(ctx, uid)
	if err != nil {
		log.Printf("server: product list after remove failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data.ShopName = vrow.ShopName
	data.Products = products
	data.Flash = "Listing removed."
	a.renderAs(w, "vendor_products", http.StatusOK, data)
}

// auditProduct writes one product.* audit row. Bookkeeping failure never
// strands a valid listing change — loud log, same policy as the vendor
// request trail (vendor_handlers.go).
func (a *App) auditProduct(ctx context.Context, actorID pgtype.UUID, action string, p *ProductRecord) {
	if p == nil || a.auth == nil {
		return
	}
	details, _ := json.Marshal(map[string]any{
		"product_id": uuidString(p.ID),
		"title":      p.Title,
		"price_sat":  p.PriceSat,
		"status":     p.Status,
	})
	if err := a.auth.AuditAppend(ctx, actorID, "user", action, "product:"+uuidString(p.ID), string(details)); err != nil {
		log.Printf("server: %s audit write failed (non-fatal): %v", action, sanitizedErr(err))
	}
}

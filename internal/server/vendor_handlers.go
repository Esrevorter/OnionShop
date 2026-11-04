package server

// Phase 4 — vendor registration, admin approval, and product CRUD
// (TASKS.md "Phase 4: Catalog & Vendors"). The store interfaces live in
// auth_handlers.go (AuthStore vendor methods + the VendorStore projection
// below); this file owns the HTTP surface only. Every state-changing POST
// rides the same gate as the account pages: RequireRole middleware + CSRF
// (router stack) + requireFriction (CAPTCHA single-use + honeypot, AGENTS.md
// §3). Role promotions ALWAYS kill the promoted user's other sessions via
// InvalidateUserSessions — a privilege change must never ride an old cookie
// (AGENTS.md §4).

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	vendorPurpose   = "vendor"   // CAPTCHA purpose namespace for vendor forms
	productPurpose  = "product"  // CAPTCHA purpose namespace for product forms
	maxTitleLen     = 200        // mirrors products.title CHECK (db/schema.sql)
	maxDescLen      = 20000      // mirrors products.description CHECK
	maxShopNameLen  = 60         // mirrors vendors.shop_name CHECK
	defaultListSize = 24         // catalog page size
	maxListSize     = 96         // hard cap on ?limit= to bound store load
)

// ProductRecord is one row from public.products (db/schema.sql). PriceSat is
// integer satoshis end-to-end — never a float anywhere in this stack.
type ProductRecord struct {
	ID          pgtype.UUID
	VendorID    pgtype.UUID
	CategoryID  int64 // 0 = uncategorized (nullable FK)
	Title       string
	Description string
	PriceSat    int64
	Stock       int
	ImagePath   string // "" unless served from web/static (never remote URLs)
	Status      string // draft | active | removed (schema CHECK)
}

// CategoryRecord is one row from public.categories.
type CategoryRecord struct {
	ID   int64
	Slug string
	Name string
}

// VendorStore is the catalog-facing projection of AuthStore: everything the
// storefront and vendor product-management pages read. Registering with a
// *PGAuthStore satisfies both interfaces with one pool-backed object; tests
// substitute one in-memory fake for both.
type VendorStore interface {
	CategoryList(ctx context.Context) ([]CategoryRecord, error)
	ProductCreate(ctx context.Context, p *ProductRecord) error
	ProductGet(ctx context.Context, id pgtype.UUID) (*ProductRecord, error)
	ProductUpdate(ctx context.Context, p *ProductRecord) error
	ProductSetStatus(ctx context.Context, id pgtype.UUID, status string) error
	ProductListByVendor(ctx context.Context, vendorID pgtype.UUID) ([]ProductRecord, error)
	ProductListActive(ctx context.Context, categoryID int64, limit int) ([]ProductRecord, error)
	ProductSearch(ctx context.Context, query string, limit int) ([]ProductRecord, error)
	ShopNameForID(ctx context.Context, vendorID pgtype.UUID) (string, error)
	ApprovedVendorID(ctx context.Context, userID pgtype.UUID) (*pgtype.UUID, error)
}

// ---- route wiring ----------------------------------------------------------

// registerVendorRoutes mounts the vendor/admin product surface. It is called
// from RegisterAuthRoutesFull AFTER the middleware stack exists; a nil store
// degrades loudly at request time (generic error), never at boot.
func (a *App) registerVendorRoutes(r chi.Router) {
	vendorMW := a.RequireRole("vendor", "admin")
	adminMW := a.RequireRole("admin")

	r.Method(http.MethodGet, "/vendor/register", a.RequireRole("customer")(http.HandlerFunc(a.handleVendorRequestForm)))
	r.Method(http.MethodPost, "/vendor/register", a.RequireRole("customer")(http.HandlerFunc(a.handleVendorRequestSubmit)))
	r.Method(http.MethodGet, "/vendor/products", vendorMW(http.HandlerFunc(a.handleVendorProducts)))
	r.Method(http.MethodPost, "/vendor/products", vendorMW(http.HandlerFunc(a.handleVendorProductCreate)))
	r.Method(http.MethodPost, "/vendor/products/{id}", vendorMW(http.HandlerFunc(a.handleVendorProductUpdate)))
	r.Method(http.MethodGet, "/admin/vendors", adminMW(http.HandlerFunc(a.handleAdminVendors)))
	r.Method(http.MethodPost, "/admin/vendors/{id}/approve", adminMW(http.HandlerFunc(a.handleAdminVendorApprove)))
}

// registerCatalogRoutes mounts the public storefront. Home is RE-MOUNTED here
// (chi replaces the earlier pattern) so the landing page becomes the catalog
// once Phase 4 stores are wired; without a vendor store the handler falls
// back to the minimal Phase 3 home render, keeping router tests honest.
func (a *App) registerCatalogRoutes(r chi.Router) {
	r.Get("/", a.handleCatalogHome)
	r.Get("/search", a.handleSearch)
	r.Get("/category/{slug}", a.handleCategory)
	r.Get("/product/{id}", a.handleProductPage)
	r.Get("/u/{handle}", a.handleShopPage)
}

// ---- shared guards ----------------------------------------------------------

// vendorGuard resolves the session user's APPROVED vendor row. It answers the
// failure itself (redirect / generic error) and returns ok=false in every
// non-happy case — handlers then simply return. An unapproved vendor editing
// products would let a rejected applicant trade under an unvetted shop, so
// approval is re-read from the store on every write, never trusted from the
// session role alone.
func (a *App) vendorGuard(w http.ResponseWriter, r *http.Request) (vendorID pgtype.UUID, ok bool) {
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return pgtype.UUID{}, false
	}
	uid := currentUserUUID(r.Context(), a.sm)
	if uid == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return pgtype.UUID{}, false
	}
	id, err := a.vendor.ApprovedVendorID(r.Context(), *uid)
	if err != nil {
		log.Printf("server: approved-vendor lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return pgtype.UUID{}, false
	}
	if id == nil {
		a.renderVendorState(w, r, http.StatusForbidden, "", "No approved vendor shop for this account. Request one first.")
		return pgtype.UUID{}, false
	}
	return *id, true
}

// parseProductForm reads + validates one product form into rec (existing !=
// nil keeps identity/status from the DB row). Returns a user-safe error text.
func parseProductForm(r *http.Request, existing *ProductRecord) (*ProductRecord, string) {
	title := strings.TrimSpace(r.FormValue("title"))
	desc := strings.TrimSpace(r.FormValue("description"))
	priceTxt := strings.TrimSpace(r.FormValue("price_sat"))
	stockTxt := strings.TrimSpace(r.FormValue("stock"))
	catTxt := strings.TrimSpace(r.FormValue("category_id"))

	rec := &ProductRecord{}
	if existing != nil {
		*rec = *existing
	}
	if len(title) < 3 || len(title) > maxTitleLen {
		return nil, "Title must be 3-200 characters."
	}
	if len(desc) > maxDescLen {
		return nil, "Description too long (max 20000 characters)."
	}
	price, err := strconv.ParseInt(priceTxt, 10, 64)
	if err != nil || price <= 0 {
		return nil, "Price must be a whole number of satoshis greater than zero."
	}
	stock, err := strconv.Atoi(stockTxt)
	if err != nil || stock < 0 {
		return nil, "Stock must be a whole number zero or greater."
	}
	var catID int64
	if catTxt != "" {
		catID, err = strconv.ParseInt(catTxt, 10, 64)
		if err != nil || catID <= 0 {
			return nil, "Unknown category."
		}
	}
	img := strings.TrimSpace(r.FormValue("image_path"))
	if img != "" && !strings.HasPrefix(img, "/static/") {
		// image_path is rendered into <img src>; anything but our own static
		// tree is either a remote tracker or an XSS probe (CSP would block it
		// anyway — refusing it here keeps the data clean).
		return nil, "Image path must start with /static/."
	}
	rec.Title, rec.Description, rec.PriceSat, rec.Stock, rec.CategoryID, rec.ImagePath = title, desc, price, stock, catID, img
	return rec, ""
}

var productStatuses = map[string]bool{"draft": true, "active": true, "removed": true}

// ---- vendor request flow -----------------------------------------------------

func (a *App) handleVendorRequestForm(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil || a.vendor == nil {
		writeGenericError(w)
		return
	}
	uid := currentUserUUID(r.Context(), a.sm)
	if uid == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	v, err := a.auth.VendorGetByUser(r.Context(), *uid)
	if err != nil {
		log.Printf("server: vendor state read failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if v != nil {
		a.renderVendorState(w, r, http.StatusOK, v.ShopName, vendorStateText(v.Approved))
		return
	}
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, vendorPurpose); data.Captcha == nil {
		return
	}
	a.render(w, "vendor_request", http.StatusOK, data)
}

func vendorStateText(approved bool) string {
	if approved {
		return "Your shop is approved. Manage products from your vendor dashboard."
	}
	return "Your application is pending administrator approval."
}

func (a *App) handleVendorRequestSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, vendorPurpose); data.Captcha == nil {
			return
		}
		a.render(w, "vendor_request", http.StatusOK, data)
	}
	if !a.requireFriction(w, r, vendorPurpose) {
		return
	}
	uid := currentUserUUID(ctx, a.sm)
	if a.auth == nil || a.vendor == nil || uid == nil {
		writeGenericError(w)
		return
	}
	name := strings.TrimSpace(r.FormValue("shop_name"))
	if len(name) < 3 || len(name) > maxShopNameLen {
		fail("Shop name must be 3-60 characters.")
		return
	}
	// Re-check via the store (never trust the session role claim alone). The
	// authoritative USERS row is fetched by the session's user id — a forged
	// or stale username claim can never steer this lookup.
	u, err := a.auth.AuthUserByID(ctx, *uid)
	if err != nil || u == nil {
		fail("Account not found. Log in again and retry.")
		return
	}
	if u.Role != "customer" || u.Status != "active" {
		writeGenericError(w) // banned/promoted riding an old cookie — no detail leak
		return
	}
	existing, err := a.auth.VendorGetByUser(ctx, u.ID)
	if err != nil {
		log.Printf("server: vendor pre-check failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if existing != nil {
		a.renderVendorState(w, r, http.StatusOK, existing.ShopName, vendorStateText(existing.Approved))
		return
	}
	if err := a.auth.VendorCreateRequest(ctx, u.ID, name); err != nil {
		// vendors.user_id/shop_name are UNIQUE: a double-submit lands here.
		// Show the state page instead of a raw constraint error.
		again, gerr := a.auth.VendorGetByUser(ctx, u.ID)
		if gerr == nil && again != nil {
			a.renderVendorState(w, r, http.StatusOK, again.ShopName, vendorStateText(again.Approved))
			return
		}
		log.Printf("server: vendor request create failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	_ = a.auth.AuditAppend(ctx, u.ID, "user", "vendor.request", uuidString(u.ID), "")
	data.Flash = "Application submitted. An administrator will review it."
	a.render(w, "vendor_request", http.StatusOK, data)
}

func (a *App) renderVendorState(w http.ResponseWriter, r *http.Request, code int, shop, msg string) {
	data := a.baseData(r)
	data.Flash = msg
	if shop != "" {
		data.Error = "" // flash carries the message; Error stays for real faults
	}
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	a.render(w, "vendor_state", code, data)
}

// ---- admin approval ----------------------------------------------------------

func (a *App) handleAdminVendors(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeGenericError(w)
		return
	}
	pending, err := a.auth.VendorListPending(r.Context())
	if err != nil {
		log.Printf("server: pending vendor list failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data := a.baseData(r)
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	data.Pending = pending
	a.render(w, "admin_vendors", http.StatusOK, data)
}

func (a *App) handleAdminVendorApprove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	if !a.requireFriction(w, r, vendorPurpose) {
		return
	}
	if a.auth == nil {
		writeGenericError(w)
		return
	}
	vid, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		writeGenericError(w)
		return
	}
	// Resolve the ROW FIRST so a bad id can never trigger a half-applied
	// promote/approve pair.
	target, err := a.auth.VendorGetByID(ctx, vid)
	if err != nil {
		log.Printf("server: vendor resolve failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if target == nil || target.Approved {
		http.Redirect(w, r, "/admin/vendors", http.StatusSeeOther)
		return
	}
	if err := a.auth.VendorSetApproval(ctx, vid, true); err != nil {
		log.Printf("server: vendor approval write failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if err := a.auth.VendorSetRoleToVendor(ctx, target.UserID); err != nil {
		log.Printf("server: vendor role promotion failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	// THE point of Phase 4 security-wise: the promoted user's OTHER sessions
	// still carry role=customer in their payloads; scs cannot edit them
	// server-side, so deletion is the only correct primitive (AGENTS.md §4).
	if err := a.InvalidateUserSessions(ctx, uuidString(target.UserID)); err != nil {
		log.Printf("server: session invalidation after approval failed: %v", sanitizedErr(err))
	}
	actor := currentUserUUID(ctx, a.sm)
	if actor != nil {
		who, _ := a.auth.VendorUsername(ctx, target.UserID) // "" on unknown ids, bookkeeping only
		_ = a.auth.AuditAppend(ctx, *actor, "admin", "vendor.approve", uuidString(vid), who)
	}
	http.Redirect(w, r, "/admin/vendors", http.StatusSeeOther)
}

// ---- vendor product CRUD ------------------------------------------------------

func (a *App) handleVendorProducts(w http.ResponseWriter, r *http.Request) {
	vendorID, ok := a.vendorGuard(w, r)
	if !ok {
		return
	}
	products, err := a.vendor.ProductListByVendor(r.Context(), vendorID)
	if err != nil {
		log.Printf("server: vendor product list failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	cats, err := a.vendor.CategoryList(r.Context())
	if err != nil {
		log.Printf("server: category list failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data := a.baseData(r)
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	data.Products = products
	data.Categories = cats
	if data.Captcha = a.newChallenge(w, r, productPurpose); data.Captcha == nil {
		return
	}
	a.render(w, "vendor_products", http.StatusOK, data)
}

func (a *App) handleVendorProductCreate(w http.ResponseWriter, r *http.Request) {
	vendorID, ok := a.vendorGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !a.requireFriction(w, r, productPurpose) {
		return
	}
	status := r.FormValue("status")
	if !productStatuses[status] {
		status = "draft"
	}
	rec, errMsg := parseProductForm(r, nil)
	if errMsg != "" {
		a.flashVendorProducts(w, r, errMsg)
		return
	}
	rec.VendorID, rec.Status = vendorID, status
	if err := a.vendor.ProductCreate(ctx, rec); err != nil {
		log.Printf("server: product create failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if actor := currentUserUUID(ctx, a.sm); actor != nil {
		_ = a.auth.AuditAppend(ctx, *actor, "vendor", "product.create", uuidString(rec.ID), "")
	}
	a.flashVendorProducts(w, r, "")
}

func (a *App) handleVendorProductUpdate(w http.ResponseWriter, r *http.Request) {
	vendorID, ok := a.vendorGuard(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !a.requireFriction(w, r, productPurpose) {
		return
	}
	pid, valid := parseUUID(chi.URLParam(r, "id"))
	if !valid {
		writeGenericError(w)
		return
	}
	existing, err := a.vendor.ProductGet(ctx, pid)
	if err != nil {
		if errors.Is(err, ErrNoSuchUser) { // sentinel reuse: unknown product id
			a.flashVendorProducts(w, r, "No such product.")
			return
		}
		log.Printf("server: product fetch failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if existing == nil || existing.VendorID != vendorID {
		// Someone else's product (or gone): uniform refusal, no existence hint.
		a.flashVendorProducts(w, r, "You may only edit your own products.")
		return
	}
	rec, errMsg := parseProductForm(r, existing)
	if errMsg != "" {
		a.flashVendorProducts(w, r, errMsg)
		return
	}
	if status := r.FormValue("status"); productStatuses[status] {
		rec.Status = status
	}
	if err := a.vendor.ProductUpdate(ctx, rec); err != nil {
		log.Printf("server: product update failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if actor := currentUserUUID(ctx, a.sm); actor != nil {
		_ = a.auth.AuditAppend(ctx, *actor, "vendor", "product.update", uuidString(pid), "")
	}
	a.flashVendorProducts(w, r, "")
}

// flashVendorProducts re-renders the dashboard with a flash/error line. The
// friction partial on that page has no CAPTCHA slot by design (the guard POST
// already burned the puzzle; re-minting per redirect loop would train bots to
// expect fresh puzzles per click), so the re-render carries CSRF + honeypot
// only.
func (a *App) flashVendorProducts(w http.ResponseWriter, r *http.Request, msg string) {
	vendorID, ok := a.vendorGuard(w, r)
	if !ok {
		return
	}
	products, err := a.vendor.ProductListByVendor(r.Context(), vendorID)
	if err != nil {
		writeGenericError(w)
		return
	}
	cats, err := a.vendor.CategoryList(r.Context())
	if err != nil {
		writeGenericError(w)
		return
	}
	data := a.baseData(r)
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	data.Products = products
	data.Categories = cats
	if msg != "" {
		data.Error = msg
	} else {
		data.Flash = "Saved."
	}
	a.render(w, "vendor_products", http.StatusOK, data)
}

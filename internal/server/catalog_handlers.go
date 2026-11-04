package server

// Phase 4 — public storefront: catalog home, category browsing, search,
// product detail, and vendor shop pages (TASKS.md "Phase 4: Catalog &
// Vendors"). All read-only; no friction needed (GETs carry no state change).
// Vendor-authored text renders through html/template's automatic escaping —
// the same rule as every other page (SECURITY_AND_OPSEC.md: treat ALL stored
// strings as untrusted input at render time).
//
// This revision rides the VendorStore interface from catalog_store.go
// (CatalogListCategories / CatalogListProducts / CatalogGetProduct /
// CatalogGetVendorByUsername / CatalogListTopVendors) instead of the older
// ProductList*/CategoryList surface.

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

const (
	// defaultListSize / maxListSize clamp ?limit= so a crafted query can never
	// ask the DB for the whole products table.
	defaultListSize = 24
	maxListSize     = 100

	catalogPageLimit = 20 // pagination window for category/search pages
)

// CatalogData extends AuthData with the storefront fields. Keeping it a
// superset struct means layout.html keeps working unchanged (it only reads
// CsrfToken/Error/Flash), and templates handed either shape still render.
type CatalogData struct {
	AuthData
	Categories   []CategoryRecord
	Products     []StorefrontProduct
	Product      *StorefrontProduct
	TopVendors   []TopVendorRow
	ShopName     string
	Vendor       *VendorRecord
	Query        string
	CategoryName string
	SatToBTC     string // pre-formatted price display helper (sat -> BTC text)
	Total        int64  // full match count for the current listing (pagination)
	Limit        int
	Offset       int
}

// satToBTC renders an integer-satoshi price as an 8-decimal BTC string. Pure
// formatting — arithmetic never leaves the integer domain anywhere else in
// the stack (ARCHITECTURE.md §5).
func satToBTC(sat int64) string {
	neg := ""
	if sat < 0 {
		neg = "-"
		sat = -sat
	}
	whole := strconv.FormatInt(sat/100000000, 10)
	frac := strconv.FormatInt(sat%100000000, 10)
	for len(frac) < 8 {
		frac = "0" + frac
	}
	return neg + whole + "." + frac
}

// catalogBase seeds CatalogData with the shared auth fields so handlers can
// fill in list slots without repeating themselves. The anti-phishing phrase
// lives in the session (phraseKey), mirroring handleAccount's read.
func (a *App) catalogBase(r *http.Request) *CatalogData {
	d := &CatalogData{AuthData: a.baseData(r)}
	d.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	return d
}

// renderCatalog mirrors render()'s policy — buffer FIRST, then commit
// headers — for the *CatalogData shape (render() takes AuthData by value and
// would silently drop the embedded struct's siblings). An execution error
// after headers are out is unrecoverable and would leak a truncated 200
// instead of the generic 500. When the storefront template set is absent
// (bare dev/test apps only parse web/templates' page_* files), fall back to
// handleHome so GET / keeps answering 200 exactly like the router tests
// assert.
func (a *App) renderCatalog(w http.ResponseWriter, r *http.Request, name string, code int, data *CatalogData) {
	tpl := a.templates[name]
	if tpl == nil {
		a.handleHome(w, r)
		return
	}
	a.renderAs(w, name, code, data)
}

// listLimit clamps ?limit= to sane bounds so a crafted query cannot ask the
// DB for the whole table.
func listLimit(r *http.Request) int {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxListSize {
				return maxListSize
			}
			return n
		}
	}
	return defaultListSize
}

// listOffset parses ?offset= defensively: garbage or negatives page zero.
func listOffset(r *http.Request) int {
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// registerCatalogRoutes mounts the Phase 4 public storefront. It runs inside
// RegisterAuthRoutesFull AFTER the middleware stack exists, replacing the
// Phase 3 "/" landing handler when (and only when) a VendorStore was wired —
// bare apps keep handleHome semantics so existing router tests pass.
func (a *App) registerCatalogRoutes(r chi.Router) {
	if a.vendor == nil {
		return
	}
	// Replace the Phase 3 landing mount with the catalog home.
	r.Get("/", a.handleCatalogHome)
	r.Get("/search", a.handleSearch)
	r.Get("/category/{slug}", a.handleCategory)
	r.Get("/product/{id}", a.handleProductPage)
	r.Get("/u/{handle}", a.handleShopPage)
}

// handleCatalogHome replaces the Phase 3 landing page when a vendor store is
// wired. Without one it MUST behave exactly like handleHome did (router tests
// assert GET / = 200 on bare apps).
func (a *App) handleCatalogHome(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		a.handleHome(w, r)
		return
	}
	ctx := r.Context()
	data := a.catalogBase(r)
	cats, err := a.vendor.CatalogListCategories(ctx)
	if err != nil {
		log.Printf("server: catalog categories failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	prods, err := a.vendor.CatalogListProducts(ctx, -1, "", listLimit(r), 0)
	if err != nil {
		log.Printf("server: catalog listing failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	// Leaderboard widget reads the cron-maintained snapshot; a missing board
	// (fresh install before first refresh) is not an error worth a 500.
	top, err := a.vendor.CatalogListTopVendors(ctx, DefaultLeaderboardSize)
	if err != nil {
		log.Printf("server: top vendors read failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Categories, data.Products, data.TopVendors = cats, prods, top
	data.Limit, data.Offset = listLimit(r), 0
	a.renderCatalog(w, r, "catalog_home", http.StatusOK, data)
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		a.handleHome(w, r)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := a.catalogBase(r)
	data.Query = q
	data.Limit, data.Offset = listLimit(r), listOffset(r)
	if q == "" {
		a.renderCatalog(w, r, "search", http.StatusOK, data)
		return
	}
	prods, err := a.vendor.CatalogListProducts(r.Context(), -1, q, data.Limit, data.Offset)
	if err != nil {
		log.Printf("server: catalog search failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	total, err := a.vendor.CatalogCountProducts(r.Context(), -1, q)
	if err != nil {
		log.Printf("server: catalog count failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data.Products, data.Total = prods, total
	a.renderCatalog(w, r, "search", http.StatusOK, data)
}

func (a *App) handleCategory(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	cats, err := a.vendor.CatalogListCategories(ctx)
	if err != nil {
		log.Printf("server: category resolve failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	var catID int64
	found := false
	for _, c := range cats {
		if c.Slug == slug {
			catID, found = c.ID, true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	limit, offset := listLimit(r), listOffset(r)
	prods, err := a.vendor.CatalogListProducts(ctx, catID, "", limit, offset)
	if err != nil {
		log.Printf("server: category listing failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	total, err := a.vendor.CatalogCountProducts(ctx, catID, "")
	if err != nil {
		log.Printf("server: category count failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data := a.catalogBase(r)
	data.Categories = cats
	data.Products = prods
	data.CategoryName = slug
	data.Limit, data.Offset, data.Total = limit, offset, total
	a.renderCatalog(w, r, "category", http.StatusOK, data)
}

func (a *App) handleProductPage(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		http.NotFound(w, r)
		return
	}
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := a.vendor.CatalogGetProduct(r.Context(), id)
	// Drafts/removed rows are invisible to the public — 404, never 403, so a
	// probe cannot distinguish "exists but hidden" from "does not exist".
	// The store already gates status='active' AND vendor approved.
	if err != nil {
		if errorsIsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("server: product fetch failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if p.Status != "active" {
		http.NotFound(w, r)
		return
	}
	data := a.catalogBase(r)
	data.Product = p
	data.ShopName = p.ShopName
	data.SatToBTC = satToBTC(p.PriceSat)
	a.renderCatalog(w, r, "product", http.StatusOK, data)
}

// handleShopPage serves /u/{handle} — the vendor profile page keyed by the
// vendor's USERNAME (CatalogGetVendorByUsername joins users and admits
// approved vendors only). Products arrive via the category-independent
// storefront listing filtered through the vendor's username in memory over
// one bounded page window; rating/sales badges ride the leaderboard snapshot
// when the vendor appears on it.
func (a *App) handleShopPage(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	handle := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "handle")))
	v, err := a.vendor.CatalogGetVendorByUsername(ctx, handle)
	if err != nil {
		if errorsIsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("server: shop resolve failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if v == nil || !v.Approved {
		http.NotFound(w, r)
		return
	}
	prods, err := a.vendor.CatalogListProducts(ctx, -1, "", maxListSize, 0)
	if err != nil {
		log.Printf("server: shop products failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	own := make([]StorefrontProduct, 0, len(prods))
	for _, p := range prods {
		if uuidString(p.VendorID) == uuidString(v.ID) {
			own = append(own, p)
		}
	}
	data := a.catalogBase(r)
	data.Vendor = v
	data.ShopName = v.ShopName
	data.Products = own
	a.renderCatalog(w, r, "shop", http.StatusOK, data)
}

// errorsIsNotFound keeps the handler 404 mapping terse; ErrNotFound is the
// only sentinel the catalog store emits for missing rows.
func errorsIsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrNotFound.Error())
}

var _ = catalogPageLimit // reserved: pagination window knob for Phase 5 listing UI

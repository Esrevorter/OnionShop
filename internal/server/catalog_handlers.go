package server

// Phase 4 — public storefront: catalog home, category browsing, search,
// product detail, and vendor shop pages (TASKS.md "Phase 4: Catalog &
// Vendors"). All read-only; no friction needed (GETs carry no state change).
// Vendor-authored text renders through html/template's automatic escaping —
// the same rule as every other page (SECURITY_AND_OPSEC.md: treat ALL stored
// strings as untrusted input at render time).

import (
	"bytes"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// CatalogData extends AuthData with the storefront fields. Keeping it a
// superset struct means layout.html keeps working unchanged (it only reads
// CsrfToken/PersonalPhrase/Error/Flash).
type CatalogData struct {
	AuthData
	Categories   []CategoryRecord
	Products     []ProductRecord
	Product      *ProductRecord
	ShopName     string
	Query        string
	CategoryName string
	SatToBTC     string // pre-formatted price display helper (sat -> BTC text)
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
	return neg + strconv.FormatInt(sat/100000000, 10) + "." +
		strings.padStart(strconv.FormatInt(sat%100000000, 10), 8, "0")
}

// catalogBase seeds CatalogData with the shared auth fields so handlers can
// fill in list slots without repeating themselves.
func (a *App) catalogBase(r *http.Request) CatalogData {
	d := CatalogData{AuthData: a.baseData(r)}
	d.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	return d
}

// renderCatalog mirrors render() — buffer FIRST, then commit headers — for
// the CatalogData shape. An execution error after headers are out is
// unrecoverable and would leak a truncated 200 instead of the generic 500.
func (a *App) renderCatalog(w http.ResponseWriter, name string, code int, data CatalogData) {
	tpl := a.templates[name]
	if tpl == nil {
		writeGenericError(w)
		return
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("server: render catalog %q failed: %v", name, err)
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
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

// handleCatalogHome replaces the Phase 3 landing page when a vendor store is
// wired. Without one it MUST behave exactly like handleHome did (router tests
// assert GET / = 200 on bare apps).
func (a *App) handleCatalogHome(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil || a.auth == nil {
		a.handleHome(w, r)
		return
	}
	data := a.catalogBase(r)
	cats, err := a.vendor.CategoryList(r.Context())
	if err != nil {
		log.Printf("server: catalog categories failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	prods, err := a.vendor.ProductListActive(r.Context(), 0, listLimit(r))
	if err != nil {
		log.Printf("server: catalog listing failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data.Categories, data.Products = cats, prods
	a.renderCatalog(w, "catalog_home", http.StatusOK, data)
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		a.handleHome(w, r)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := a.catalogBase(r)
	data.Query = q
	if q == "" {
		a.renderCatalog(w, "search", http.StatusOK, data)
		return
	}
	prods, err := a.vendor.ProductSearch(r.Context(), q, listLimit(r))
	if err != nil {
		log.Printf("server: catalog search failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data.Products = prods
	a.renderCatalog(w, "search", http.StatusOK, data)
}

func (a *App) handleCategory(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		http.NotFound(w, r)
		return
	}
	slug := chi.URLParam(r, "slug")
	cats, err := a.vendor.CategoryList(r.Context())
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
	prods, err := a.vendor.ProductListActive(r.Context(), catID, listLimit(r))
	if err != nil {
		log.Printf("server: category listing failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data := a.catalogBase(r)
	data.Categories = cats
	data.Products = prods
	data.CategoryName = slug
	a.renderCatalog(w, "category", http.StatusOK, data)
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
	p, err := a.vendor.ProductGet(r.Context(), id)
	if err != nil {
		log.Printf("server: product fetch failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	// Drafts/removed rows are invisible to the public — 404, never 403, so a
	// probe cannot distinguish "exists but hidden" from "does not exist".
	if p == nil || p.Status != "active" {
		http.NotFound(w, r)
		return
	}
	shop, err := a.vendor.ShopNameForID(r.Context(), p.VendorID)
	if err != nil {
		log.Printf("server: shop name fetch failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data := a.catalogBase(r)
	data.Product = p
	data.ShopName = shop
	data.SatToBTC = satToBTC(p.PriceSat)
	a.renderCatalog(w, "product", http.StatusOK, data)
}

// handleShopPage serves /u/<handle> — the vendor profile. Rating/sales/ETA
// badges and "message vendor" arrive with Phases 5-7; this ships the approved
// vendor's active listings under their shop identity.
func (a *App) handleShopPage(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil || a.auth == nil {
		http.NotFound(w, r)
		return
	}
	handle := strings.ToLower(chi.URLParam(r, "handle"))
	// The handle IS the shop name (vendors.shop_name UNIQUE); look the vendor
	// up by scanning approved shops via the username join already used by the
	// admin queue. ShopNameForID's inverse needs a store method:
	v, err := a.auth.VendorByShopName(r.Context(), handle)
	if err != nil {
		log.Printf("server: shop resolve failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if v == nil || !v.Approved {
		http.NotFound(w, r)
		return
	}
	prods, err := a.vendor.ProductListByVendor(r.Context(), v.ID)
	if err != nil {
		log.Printf("server: shop products failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	active := make([]ProductRecord, 0, len(prods))
	for _, p := range prods {
		if p.Status == "active" {
			active = append(active, p)
		}
	}
	data := a.catalogBase(r)
	data.ShopName = v.ShopName
	data.Products = active
	a.renderCatalog(w, "shop", http.StatusOK, data)
}

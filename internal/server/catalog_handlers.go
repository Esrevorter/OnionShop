package server

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

const (
	defaultListSize  = 24
	maxListSize      = 100
	catalogPageLimit = 20
)

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
	DisplayPrice string
	Total        int64
	Limit        int
	Offset       int
	PrevOffset   int
	NextOffset   int
	ShopHandle   string
}

func formatDisplayPrice(sat int64) string {
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

func (a *App) catalogBase(r *http.Request) *CatalogData {
	d := &CatalogData{AuthData: a.baseData(r)}
	d.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	return d
}

func (a *App) renderCatalog(w http.ResponseWriter, r *http.Request, name string, code int, data *CatalogData) {
	tpl := a.templates[name]
	if tpl == nil {
		a.handleHome(w, r)
		return
	}
	a.renderAs(w, name, code, data)
}

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

func (d *CatalogData) paginationData() {
	d.PrevOffset = d.Offset - d.Limit
	if d.PrevOffset < 0 {
		d.PrevOffset = 0
	}
	d.NextOffset = d.Offset + d.Limit
}

func listOffset(r *http.Request) int {
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func (a *App) registerCatalogRoutes(r chi.Router) {
	if a.vendor == nil {
		return
	}
	r.Get("/", a.handleCatalogHome)
	r.Get("/search", a.handleSearch)
	r.Get("/category/{slug}", a.handleCategory)
	r.Get("/product/{id}", a.handleProductPage)
	r.Get("/u/{handle}", a.handleShopPage)
}

func (a *App) handleCatalogHome(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		a.handleHome(w, r)
		return
	}
	ctx := r.Context()
	data := a.catalogBase(r)
	cats, err := a.vendor.CatalogListCategories(ctx)
	if err != nil {
		log.Printf("server: catalog categories failed: %v", err)
		writeGenericError(w)
		return
	}
	prods, err := a.vendor.CatalogListProducts(ctx, -1, "", listLimit(r), 0)
	if err != nil {
		log.Printf("server: catalog listing failed: %v", err)
		writeGenericError(w)
		return
	}
	top, err := a.vendor.CatalogListTopVendors(ctx, DefaultLeaderboardSize)
	if err != nil {
		log.Printf("server: top vendors read failed (non-fatal): %v", err)
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
		log.Printf("server: catalog search failed: %v", err)
		writeGenericError(w)
		return
	}
	total, err := a.vendor.CatalogCountProducts(r.Context(), -1, q)
	if err != nil {
		log.Printf("server: catalog count failed: %v", err)
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
		log.Printf("server: category resolve failed: %v", err)
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
		log.Printf("server: category listing failed: %v", err)
		writeGenericError(w)
		return
	}
	total, err := a.vendor.CatalogCountProducts(ctx, catID, "")
	if err != nil {
		log.Printf("server: category count failed: %v", err)
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
	if err != nil {
		if errorsIsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("server: product fetch failed: %v", err)
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
	data.DisplayPrice = formatDisplayPrice(p.PriceSat)
	a.renderCatalog(w, r, "product", http.StatusOK, data)
}

// handleShopPage serves /u/{handle}. It populates ShopHandle (the message
// form's action target) and mints a CAPTCHA bound to messagePurpose, because
// the embedded "message vendor" form requires friction on POST. Without the
// challenge here the form would demand verification with none shown.
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
		log.Printf("server: shop resolve failed: %v", err)
		writeGenericError(w)
		return
	}
	if v == nil || !v.Approved {
		http.NotFound(w, r)
		return
	}
	prods, err := a.vendor.CatalogListProducts(ctx, -1, "", maxListSize, 0)
	if err != nil {
		log.Printf("server: shop products failed: %v", err)
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
	data.ShopHandle = handle
	data.Products = own
	if data.Captcha = a.newChallenge(w, r, messagePurpose); data.Captcha == nil {
		return
	}
	a.renderCatalog(w, r, "shop", http.StatusOK, data)
}

func errorsIsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrNotFound.Error())
}

var _ = catalogPageLimit

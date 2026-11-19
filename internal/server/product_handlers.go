package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	productPurpose = "product"
	productTitleMin, productTitleMax = 3, 200
	productDescMax = 20000
	productStatusDraft, productStatusActive, productStatusRemoved = "draft", "active", "removed"
	maxUploadSize = 10 << 20
	maxFiles = 5
	uploadDir = "/var/lib/onionshop/uploads"
)

type ProductFormData struct {
	AuthData
	Products   []ProductRecord
	Categories []CategoryRecord
	Editing    *ProductRecord
	ShopName   string
}

func (a *App) productData(r *http.Request) *ProductFormData {
	return &ProductFormData{AuthData: a.baseData(r)}
}

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

func (a *App) registerProductRoutes(r chi.Router) {
	vendorOrAdmin := a.RequireRole("vendor", "admin")
	r.Method(http.MethodGet, "/vendor/products", vendorOrAdmin(http.HandlerFunc(a.handleProductList)))
	r.Method(http.MethodGet, "/vendor/products/new", vendorOrAdmin(http.HandlerFunc(a.handleProductNew)))
	r.Method(http.MethodPost, "/vendor/products/new", vendorOrAdmin(http.HandlerFunc(a.handleProductCreate)))
	r.Method(http.MethodGet, "/vendor/products/{id}/edit", vendorOrAdmin(http.HandlerFunc(a.handleProductEdit)))
	r.Method(http.MethodPost, "/vendor/products/{id}/edit", vendorOrAdmin(http.HandlerFunc(a.handleProductUpdate)))
	r.Method(http.MethodPost, "/vendor/products/{id}/publish", vendorOrAdmin(http.HandlerFunc(a.handleProductPublish)))
	r.Method(http.MethodPost, "/vendor/products/{id}/remove", vendorOrAdmin(http.HandlerFunc(a.handleProductRemove)))
	r.Method(http.MethodPost, "/product/{id}/add-to-cart", a.RequireRole("customer", "vendor", "mod", "admin")(http.HandlerFunc(a.handleAddToCart)))

}

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
		log.Printf("server: category list failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Products, data.Categories = products, cats
	a.renderAs(w, "vendor_products", http.StatusOK, data)
}

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
		VendorID: vendorID, CategoryID: catID, Title: title, Description: desc,
		PriceSat: price, Stock: stock, Status: status,
	}, ""
}

func generateRandomFilename(ext string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b) + ext
}

// deletePublicImages removes on-disk full+thumb for each public path, validated
// to stay inside uploadDir. Used when an image is replaced.
func deletePublicImages(publicPaths []string) {
	for _, pp := range publicPaths {
		pp = strings.TrimSpace(pp)
		if pp == "" || !strings.HasPrefix(pp, "/uploads/") {
			continue
		}
		name := filepath.Base(pp)
		if name == "." || name == "/" || name == "\\" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			continue
		}
		_ = os.Remove(filepath.Join(uploadDir, name))
		_ = os.Remove(filepath.Join(uploadDir, strings.TrimSuffix(name, filepath.Ext(name))+"_thumb"+ext))
	}
}

// handleImageUploads decodes+re-encodes (EXIF scrub), writes a capped full and a
// 480px thumb, returns a single public path, and deletes replaced files.
func handleImageUploads(r *http.Request, existingPaths string) (string, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File["images"]) == 0 {
		return existingPaths, nil
	}
	files := r.MultipartForm.File["images"]
	if len(files) > maxFiles {
		return "", fmt.Errorf("maximum %d images allowed", maxFiles)
	}

	var newPaths []string
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return "", fmt.Errorf("failed to prepare upload directory")
	}

	for _, fileHeader := range files {
		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			return "", fmt.Errorf("invalid file type: %s (allowed: jpg, jpeg, png)", ext)
		}
		file, err := fileHeader.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open uploaded file")
		}
		img, format, err := image.Decode(file)
		file.Close()
		if err != nil {
			return "", fmt.Errorf("invalid or corrupted image data")
		}
		outExt := ".jpg"
		if format == "png" {
			outExt = ".png"
		}
		safeName := generateRandomFilename(outExt)
		destPath := filepath.Join(uploadDir, safeName)
		dst, err := os.Create(destPath)
		if err != nil {
			return "", fmt.Errorf("failed to create destination file")
		}
		err = encodeScaled(dst, img, outExt, fullMaxEdge, 90)
		dst.Close()
		if err != nil {
			os.Remove(destPath)
			return "", fmt.Errorf("failed to re-encode and scrub image metadata")
		}
		thumbName := strings.TrimSuffix(safeName, outExt) + "_thumb" + outExt
		thumbPath := filepath.Join(uploadDir, thumbName)
		tdst, err := os.Create(thumbPath)
		if err != nil {
			os.Remove(destPath)
			return "", fmt.Errorf("failed to create thumbnail file")
		}
		if err := encodeScaled(tdst, img, outExt, thumbMaxEdge, 82); err != nil {
			tdst.Close()
			os.Remove(thumbPath)
			os.Remove(destPath)
			return "", fmt.Errorf("failed to build thumbnail")
		}
		tdst.Close()
		newPaths = append(newPaths, safeName)
	}

	if len(newPaths) > 0 {
		if existingPaths != "" {
			deletePublicImages(strings.Split(existingPaths, ","))
		}
		for _, extra := range newPaths[1:] {
			_ = os.Remove(filepath.Join(uploadDir, extra))
			_ = os.Remove(filepath.Join(uploadDir, strings.TrimSuffix(extra, filepath.Ext(extra))+"_thumb"+strings.ToLower(filepath.Ext(extra))))
		}
		return "/uploads/" + newPaths[0], nil
	}
	return existingPaths, nil
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
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		fail("invalid form data or upload too large (max 10MB)")
		return
	}
	p, msg := a.parseProductForm(r, vrow.ID)
	if msg != "" {
		fail(msg)
		return
	}
	imgPaths, err := handleImageUploads(r, "")
	if err != nil {
		fail(err.Error())
		return
	}
	p.ImagePath = imgPaths
	created, err := a.vendor.CatalogCreateProduct(ctx, *p)
	if err != nil {
		log.Printf("server: product create failed: %v", sanitizedErr(err))
		fail("listing failed, please try again")
		return
	}
	a.auditProduct(ctx, uid, actionProductCreate, created)
	http.Redirect(w, r, "/vendor/products", http.StatusSeeOther)
}

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
		http.NotFound(w, r)
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
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		fail(http.StatusBadRequest, "invalid form data or upload too large (max 10MB)")
		return
	}
	p, msg := a.parseProductForm(r, vrow.ID)
	if msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}
	p.ID = existing.ID
	imgPaths, err := handleImageUploads(r, existing.ImagePath)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	p.ImagePath = imgPaths
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
	http.Redirect(w, r, "/vendor/products", http.StatusSeeOther)
}

func (a *App) handleProductPublish(w http.ResponseWriter, r *http.Request) {
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
	if honeypotTriggered(r) || !a.requireFriction(w, r, productPurpose) {
		a.handleProductList(w, r)
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
		log.Printf("server: product publish lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	if existing.Status == productStatusRemoved {
		http.NotFound(w, r)
		return
	}
	if existing.Status == productStatusActive {
		a.handleProductList(w, r)
		return
	}
	existing.Status = productStatusActive
	updated, err := a.vendor.CatalogUpdateProduct(ctx, *existing)
	if err != nil {
		log.Printf("server: product publish write failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	a.auditProduct(ctx, uid, actionProductUpdate, updated)
	vrow, err := a.vendorRow(ctx, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	products, err := a.vendor.CatalogListVendorProducts(ctx, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	data.ShopName = vrow.ShopName
	data.Products = products
	data.Flash = "Listing published. It is now visible on the marketplace."
	a.renderAs(w, "vendor_products", http.StatusOK, data)
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
	if honeypotTriggered(r) || !a.requireFriction(w, r, productPurpose) {
		a.handleProductList(w, r)
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
	vrow, err := a.vendorRow(ctx, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	products, err := a.vendor.CatalogListVendorProducts(ctx, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	data.ShopName = vrow.ShopName
	data.Products = products
	data.Flash = "Listing removed."
	a.renderAs(w, "vendor_products", http.StatusOK, data)
}

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

// handleAddToCart adds a product to the user's session-based cart.
func (a *App) handleAddToCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	if uid == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	pid, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Get the product to verify it exists and is active
	p, err := a.vendor.CatalogGetProduct(ctx, pid)
	if err != nil {
		if errorsIsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		log.Printf("server: add to cart product lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}

	if p.Status != "active" {
		http.NotFound(w, r)
		return
	}

	// Simple session-based cart implementation
	cartData := a.sm.GetString(ctx, "cart")
	var items []string
	if cartData != "" {
		items = strings.Split(cartData, ",")
	}
	
	// Check if already in cart
	found := false
	for _, item := range items {
		if item == uuidString(pid) {
			found = true
			break
		}
	}
	
	if !found {
		items = append(items, uuidString(pid))
		a.sm.Put(ctx, "cart", strings.Join(items, ","))
	}

	// Redirect back to product page
	a.sm.Put(ctx, "flash", "Added to cart!")
	http.Redirect(w, r, "/product/"+uuidString(pid), http.StatusSeeOther)
}


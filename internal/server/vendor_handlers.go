package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	actionVendorRequest = "vendor.request"
	actionVendorApprove = "vendor.approve"
	actionVendorReject  = "vendor.reject"
	actionProductCreate = "product.create"
	actionProductUpdate = "product.update"
	actionProductRemove = "product.remove"
	actionReviewSubmit  = "review.submit"
	actionVendorMessage = "vendor.message"

	vendorPurpose  = "vendor"
	reviewPurpose  = "review"
	messagePurpose = "vmessage"

	shopNameMin, shopNameMax = 3, 60
	reviewBodyMax            = 2000
	vendorMessageMax         = 5000
)

const ReviewEditWindow = 30 * 24 * time.Hour

type VendorData struct {
	AuthData
	ShopName      string
	Pending       []VendorRecord
	Vendor        *VendorRecord
	VendorRating  float64
	VendorSales   int64
	Reviews       []ReviewRecord
	MessageTarget string
	Eta           string
}

func (a *App) vendorData(r *http.Request) *VendorData {
	return &VendorData{AuthData: a.baseData(r)}
}

func (a *App) renderAs(w http.ResponseWriter, name string, code int, data any) {
	tpl := a.templates[name]
	if tpl == nil {
		writeGenericError(w)
		return
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("server: render %q failed: %v", name, err)
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

func (a *App) currentUUID(r *http.Request) (pgtype.UUID, bool) {
	u := parseUUIDText(CurrentUserID(r.Context(), a.sm))
	return u, u.Valid
}

func (a *App) registerVendorRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	adminOnly := a.RequireRole("admin")

	r.Method(http.MethodGet, "/vendor/apply", anyUser(http.HandlerFunc(a.handleVendorApplyForm)))
	r.Method(http.MethodPost, "/vendor/apply", anyUser(http.HandlerFunc(a.handleVendorApplySubmit)))

	r.Method(http.MethodGet, "/admin/vendors", adminOnly(http.HandlerFunc(a.handleVendorQueue)))
	r.Method(http.MethodPost, "/admin/vendors/{id}/decision", adminOnly(http.HandlerFunc(a.handleVendorDecision)))

	r.Method(http.MethodGet, "/account/reviews", anyUser(http.HandlerFunc(a.handleReviewList)))
	r.Method(http.MethodPost, "/reviews/submit", anyUser(http.HandlerFunc(a.handleReviewSubmit)))

	r.Method(http.MethodPost, "/vendor/{handle}/message", anyUser(http.HandlerFunc(a.handleVendorMessage)))

	if a.vendor != nil {
		a.registerProductRoutes(r)
	}
}

// handleVendorMessage posts one note to an approved vendor from their /u/<handle>
// page. The recipient is resolved server-side from the URL handle (never a form
// id), so a crafted body cannot retarget the message. The page is re-rendered
// with a CatalogData (the same shape the GET handler uses) so the product grid
// and the message form's handle stay populated, and a FRESH challenge is minted
// on every re-render — the previous code rendered with a VendorData that lacked
// the fields the shop template needs, which 500'd the success path.
func (a *App) handleVendorMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.catalogBase(r)
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
	}
	fail := func(code int, msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, messagePurpose); data.Captcha == nil {
			return
		}
		a.renderCatalog(w, r, "shop", code, data)
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, messagePurpose) {
		fail(http.StatusUnauthorized, "challenge failed, please try again")
		return
	}
	handle := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "handle")))
	target, err := a.vendor.CatalogGetVendorByUsername(ctx, handle)
	if errors.Is(err, ErrNotFound) || target == nil {
		fail(http.StatusBadRequest, "no such vendor")
		return
	}
	if err != nil {
		log.Printf("server: vendor message target lookup failed: %v", err)
		writeGenericError(w)
		return
	}
	if !target.Approved {
		fail(http.StatusBadRequest, "no such vendor")
		return
	}
	senderName, err := a.auth.VendorUsername(ctx, uid)
	if err != nil {
		log.Printf("server: vendor message sender lookup failed: %v", err)
		writeGenericError(w)
		return
	}
	if strings.EqualFold(senderName, handle) {
		fail(http.StatusBadRequest, "cannot message yourself")
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		fail(http.StatusBadRequest, "message is empty")
		return
	}
	if len(body) > vendorMessageMax {
		fail(http.StatusBadRequest, "message too long")
		return
	}
	if err := a.vendor.CatalogSendVendorMessage(ctx, uid, target.UserID, body); err != nil {
		log.Printf("server: vendor message write failed: %v", err)
		fail(http.StatusInternalServerError, "message failed, please try again")
		return
	}
	details, _ := json.Marshal(map[string]any{"to": handle, "length": len(body)})
	if err := a.auth.AuditAppend(ctx, uid, "user", actionVendorMessage, "vendor:"+uuidString(target.ID), string(details)); err != nil {
		log.Printf("server: vendor message audit write failed (non-fatal): %v", err)
	}
	// Re-render the shop with the vendor + its products so the form stays usable.
	data.Vendor = target
	data.ShopName = target.ShopName
	data.ShopHandle = handle
	prods, err := a.vendor.CatalogListProducts(ctx, -1, "", maxListSize, 0)
	if err != nil {
		log.Printf("server: shop products failed: %v", err)
		fail(http.StatusInternalServerError, "message sent, but the shop could not refresh")
		return
	}
	own := make([]StorefrontProduct, 0, len(prods))
	for _, p := range prods {
		if uuidString(p.VendorID) == uuidString(target.ID) {
			own = append(own, p)
		}
	}
	data.Products = own
	data.Flash = "Message sent."
	if data.Captcha = a.newChallenge(w, r, messagePurpose); data.Captcha == nil {
		return
	}
	a.renderCatalog(w, r, "shop", http.StatusOK, data)
}

func (a *App) handleVendorApplyForm(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
	}
	data := a.vendorData(r)
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	existing, err := a.auth.VendorGetByUser(r.Context(), uid)
	if err != nil {
		log.Printf("server: vendor apply lookup failed: %v", err)
		writeGenericError(w)
		return
	}
	if existing != nil {
		data.Flash = "Your vendor application is already on file."
		if existing.Approved {
			data.Flash = "You are an approved vendor. Manage listings under /vendor/products."
		}
		a.renderAs(w, "vendor_apply", http.StatusOK, data)
		return
	}
	if data.Captcha = a.newChallenge(w, r, vendorPurpose); data.Captcha == nil {
		return
	}
	a.renderAs(w, "vendor_apply", http.StatusOK, data)
}

func (a *App) handleVendorApplySubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.vendorData(r)
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, vendorPurpose); data.Captcha == nil {
			return
		}
		a.renderAs(w, "vendor_apply", http.StatusBadRequest, data)
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if CurrentRole(ctx, a.sm) != "customer" {
		fail("this account cannot request vendor status")
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, vendorPurpose) {
		fail("challenge failed, please try again")
		return
	}
	shop := strings.TrimSpace(r.FormValue("shop_name"))
	if len(shop) < shopNameMin || len(shop) > shopNameMax {
		fail(fmt.Sprintf("shop name must be %d-%d characters", shopNameMin, shopNameMax))
		return
	}
	if err := a.auth.VendorCreateRequest(ctx, uid, shop); err != nil {
		log.Printf("server: vendor request write failed: %v", err)
		fail("vendor application failed, please try again")
		return
	}
	details, _ := json.Marshal(map[string]any{"shop_name": shop})
	if err := a.auth.AuditAppend(ctx, uid, "user", actionVendorRequest, "vendor:"+uuidString(uid), string(details)); err != nil {
		log.Printf("server: vendor request audit write failed (non-fatal): %v", err)
	}
	data.Flash = "Application submitted. An administrator will review it."
	a.renderAs(w, "vendor_apply", http.StatusOK, data)
}

// handleVendorQueue renders the admin approval queue. It MUST mint a CAPTCHA
// because every decision form on the page requires friction (handleVendorDecision);
// without a challenge here the admin sees "challenge required" with no challenge.
func (a *App) handleVendorQueue(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeGenericError(w)
		return
	}
	data := a.vendorData(r)
	pending, err := a.auth.VendorListPending(r.Context())
	if err != nil {
		log.Printf("server: pending vendor list failed: %v", err)
		writeGenericError(w)
		return
	}
	data.Pending = pending
	if data.Captcha = a.newChallenge(w, r, vendorPurpose); data.Captcha == nil {
		return
	}
	a.renderAs(w, "admin_vendors", http.StatusOK, data)
}

func (a *App) handleVendorDecision(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.vendorData(r)
	fail := func(code int, msg string) {
		data.Error = msg
		pending, err := a.auth.VendorListPending(ctx)
		if err != nil {
			writeGenericError(w)
			return
		}
		data.Pending = pending
		if data.Captcha = a.newChallenge(w, r, vendorPurpose); data.Captcha == nil {
			return
		}
		a.renderAs(w, "admin_vendors", code, data)
	}
	if honeypotTriggered(r) {
		fail(http.StatusUnauthorized, "challenge failed, please try again")
		return
	}
	approve := r.FormValue("decision") == "approve"
	vid, err := parseUUIDParam(r, "id")
	if err != nil {
		fail(http.StatusBadRequest, "unknown application")
		return
	}
	target, err := a.vendorRowTarget(ctx, vid)
	if err != nil {
		fail(http.StatusBadRequest, "unknown application")
		return
	}
	role, err := a.auth.AuthUserRole(ctx, target.UserID)
	if err != nil {
		log.Printf("server: vendor decision role lookup failed: %v", err)
		fail(http.StatusInternalServerError, "approval failed, please try again")
		return
	}
	if role != "customer" && approve {
		fail(http.StatusBadRequest, "unknown application")
		return
	}
	if err := a.auth.VendorSetApproval(ctx, vid, approve); err != nil {
		log.Printf("server: vendor approval write failed: %v", err)
		fail(http.StatusInternalServerError, "approval failed, please try again")
		return
	}
	if approve {
		if err := a.auth.VendorSetRoleToVendor(ctx, target.UserID); err != nil {
			log.Printf("server: vendor promotion failed: %v", err)
		}
		if err := a.InvalidateUserSessions(ctx, uuidString(target.UserID)); err != nil {
			log.Printf("server: session invalidation after vendor approval failed: %v", err)
		}
	}
	actorID, _ := a.currentUUID(r)
	name, _ := a.auth.VendorUsername(ctx, target.UserID)
	details, _ := json.Marshal(map[string]any{"shop_name": target.ShopName, "username": name})
	action := actionVendorApprove
	if !approve {
		action = actionVendorReject
	}
	if err := a.auth.AuditAppend(ctx, actorID, "admin", action, "vendor:"+uuidString(vid), string(details)); err != nil {
		log.Printf("server: vendor decision audit write failed (non-fatal): %v", err)
	}
	if approve {
		data.Flash = "Vendor approved."
	} else {
		data.Flash = "Application rejected."
	}
	fail(http.StatusOK, "")
}

func (a *App) vendorRowTarget(ctx context.Context, vendorID pgtype.UUID) (*VendorRecord, error) {
	pending, err := a.auth.VendorListPending(ctx)
	if err != nil {
		return nil, err
	}
	for i := range pending {
		if uuidString(pending[i].ID) == uuidString(vendorID) {
			return &pending[i], nil
		}
	}
	return nil, ErrNotFound
}

func (a *App) handleReviewList(w http.ResponseWriter, r *http.Request) {
	if a.vendor == nil {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	data := a.vendorData(r)
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	handle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("vendor")))
	if handle == "" {
		data.Error = "pick a vendor to review"
		a.renderAs(w, "reviews", http.StatusOK, data)
		return
	}
	prof, err := a.vendor.CatalogGetVendorByUsername(ctx, handle)
	if errors.Is(err, ErrNotFound) {
		data.Error = "no such vendor"
		a.renderAs(w, "reviews", http.StatusOK, data)
		return
	}
	if err != nil {
		log.Printf("server: review vendor lookup failed: %v", err)
		writeGenericError(w)
		return
	}
	lines, err := a.vendor.CatalogReviewableLines(ctx, uid, prof.ID)
	if err != nil {
		log.Printf("server: reviewable lines failed: %v", err)
		writeGenericError(w)
		return
	}
	data.Reviews = lines
	data.MessageTarget = handle
	if data.Captcha = a.newChallenge(w, r, reviewPurpose); data.Captcha == nil {
		return
	}
	a.renderAs(w, "reviews", http.StatusOK, data)
}

func (a *App) handleReviewSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.vendorData(r)
	fail := func(code int, msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, reviewPurpose); data.Captcha == nil {
			return
		}
		a.renderAs(w, "reviews", code, data)
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, reviewPurpose) {
		fail(http.StatusUnauthorized, "challenge failed, please try again")
		return
	}
	itemID, err := strconv.ParseInt(r.FormValue("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		fail(http.StatusBadRequest, "unknown order line")
		return
	}
	rating, err := strconv.Atoi(r.FormValue("rating"))
	if err != nil || rating < 1 || rating > 5 {
		fail(http.StatusBadRequest, "rating must be between 1 and 5")
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if len(body) > reviewBodyMax {
		fail(http.StatusBadRequest, "review text too long")
		return
	}
	err = a.vendor.CatalogSubmitReview(ctx, uid, itemID, rating, body)
	switch {
	case errors.Is(err, ErrNotFound):
		fail(http.StatusBadRequest, "that order line cannot be reviewed")
		return
	case err != nil && strings.Contains(err.Error(), "window closed"):
		fail(http.StatusBadRequest, "the review window for this order has closed")
		return
	case err != nil:
		log.Printf("server: review submit failed: %v", err)
		fail(http.StatusInternalServerError, "review failed, please try again")
		return
	}
	details, _ := json.Marshal(map[string]any{"item_id": itemID, "rating": rating})
	if aerr := a.auth.AuditAppend(ctx, uid, "user", actionReviewSubmit, fmt.Sprintf("order_item:%d", itemID), string(details)); aerr != nil {
		log.Printf("server: review audit write failed (non-fatal): %v", aerr)
	}
	data.Flash = "Review saved."
	fail(http.StatusOK, "")
}

func parseUUIDParam(r *http.Request, key string) (pgtype.UUID, error) {
	raw := chi.URLParam(r, key)
	u := parseUUIDText(raw)
	if !u.Valid {
		return pgtype.UUID{}, errors.New("server: invalid uuid parameter")
	}
	return u, nil
}

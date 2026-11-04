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

// Phase 4 vendor surface (TASKS.md "Catalog & Vendors"): registration with
// admin approval, the /u/<handle> profile pages (rating, sales count, ETA
// badge, "message vendor"), and order reviews with a bounded edit window.
// Every POST rides the same gate/friction pattern as the auth surface:
// RequireRole + CSRF (global middleware) + image CAPTCHA + honeypot, and
// every privilege/state change lands in audit_log (migration 0003).

const (
	// Audit action verbs — stable identifiers, never free text (SECURITY §3).
	actionVendorRequest = "vendor.request"
	actionVendorApprove = "vendor.approve"
	actionVendorReject  = "vendor.reject"
	actionProductCreate = "product.create"
	actionProductUpdate = "product.update"
	actionProductRemove = "product.remove"
	actionReviewSubmit  = "review.submit"
	actionVendorMessage = "vendor.message"

	vendorPurpose  = "vendor"   // CAPTCHA slot: apply / approve / reject forms
	reviewPurpose  = "review"   // CAPTCHA slot: review submit forms
	messagePurpose = "vmessage" // CAPTCHA slot: "message vendor" form

	shopNameMin, shopNameMax = 3, 60 // mirrors vendors.shop_name CHECK
	reviewBodyMax            = 2000  // mirrors reviews.body CHECK
	vendorMessageMax         = 5000  // mirrors messages.body CHECK
)

// ReviewEditWindow bounds how long after an order's last state change its
// lines stay rateable/editable. Enforced server-side (Postgres' own clock in
// the store; this constant only sizes the UI copy + the SQL interval), never
// by a form-supplied timestamp.
const ReviewEditWindow = 30 * 24 * time.Hour

// VendorData renders the vendor-facing templates (apply page, admin queue,
// profile page, review forms). It embeds AuthData so baseData stays the one
// place CSRF/phrase/flash/error are assembled, and every template keeps
// working when handed either shape.
type VendorData struct {
	AuthData
	ShopName      string
	Pending       []VendorRecord // admin queue rows
	Vendor        *VendorRecord  // profile subject
	VendorRating  float64        // avg 1..5 from released orders
	VendorSales   int64          // completed-order count
	Reviews       []ReviewRecord // customer review lines (account page)
	MessageTarget string         // username the message form posts to
	Eta           string         // fast|medium|slow badge
}

// vendorData builds the typed payload for catalog templates: baseData keeps
// assembling CSRF/phrase/flash/error in one place (auth_handlers.go), and the
// catalog extras ride on top so pages never re-derive them.
func (a *App) vendorData(r *http.Request) *VendorData {
	return &VendorData{AuthData: a.baseData(r)}
}

// renderAs is render()'s typed-payload sibling: pass an AuthData for the
// plain pages or a *VendorData whenever the template needs catalog extras
// (Pending, Reviews, profile fields). Buffering + generic-500 policy are
// identical to render() — kept separate only because render() takes AuthData
// by value and would silently drop the embedded-pointer fields' siblings.
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

// currentUUID parses the session user id back into a pgtype.UUID. Handlers
// call it instead of re-reading the DB: the value was validated at login and
// role-checked by RequireRole on this very request.
func (a *App) currentUUID(r *http.Request) (pgtype.UUID, bool) {
	u := parseUUIDText(CurrentUserID(r.Context(), a.sm))
	return u, u.Valid
}

// registerVendorRoutes mounts the Phase 4 vendor/admin/review surface. It is
// called from RegisterAuthRoutesFull AFTER the middleware stack exists; nil
// stores simply skip their routes (dev boots stay honest, never panic).
func (a *App) registerVendorRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	adminOnly := a.RequireRole("admin")

	// Vendor application: any authenticated account may ask; approval flips
	// the role through the durable store (never via this form).
	r.Method(http.MethodGet, "/vendor/apply", anyUser(http.HandlerFunc(a.handleVendorApplyForm)))
	r.Method(http.MethodPost, "/vendor/apply", anyUser(http.HandlerFunc(a.handleVendorApplySubmit)))

	// Admin approval queue (writes to audit log, TASKS.md Phase 4 line 1).
	r.Method(http.MethodGet, "/admin/vendors", adminOnly(http.HandlerFunc(a.handleVendorQueue)))
	r.Method(http.MethodPost, "/admin/vendors/{id}/decision", adminOnly(http.HandlerFunc(a.handleVendorDecision)))

	// Order reviews: list of rateable lines per vendor + one submit endpoint.
	r.Method(http.MethodGet, "/account/reviews", anyUser(http.HandlerFunc(a.handleReviewList)))
	r.Method(http.MethodPost, "/reviews/submit", anyUser(http.HandlerFunc(a.handleReviewSubmit)))

	// "Message vendor" from the public shop page (catalog_store's
	// CatalogSendVendorMessage; Phase 7 re-homes these rows into threads).
	r.Method(http.MethodPost, "/vendor/{handle}/message", anyUser(http.HandlerFunc(a.handleVendorMessage)))

	// Vendor product dashboard + CRUD (product_handlers.go). Skipped without
	// a catalog store — same nil discipline as every mount above.
	if a.vendor != nil {
		a.registerProductRoutes(r)
	}
}

// ---- message vendor -----------------------------------------------------------

// handleVendorMessage posts one note to an approved vendor from their /u/<handle>
// page. The recipient is resolved server-side from the URL handle (never a form
// id), so a crafted body cannot retarget the message. Self-messaging and unknown
// or unapproved vendors all fail with the same generic verb.
func (a *App) handleVendorMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.vendorData(r)
	fail := func(code int, msg string) {
		data.Error = msg
		a.renderAs(w, "shop", code, data)
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if a.vendor == nil || a.auth == nil {
		writeGenericError(w)
		return
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
		log.Printf("server: vendor message target lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	senderName, err := a.auth.VendorUsername(ctx, uid)
	if err != nil {
		log.Printf("server: vendor message sender lookup failed: %v", sanitizedErr(err))
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
		log.Printf("server: vendor message write failed: %v", sanitizedErr(err))
		fail(http.StatusInternalServerError, "message failed, please try again")
		return
	}
	details, _ := json.Marshal(map[string]any{"to": handle, "length": len(body)})
	if err := a.auth.AuditAppend(ctx, uid, "user", actionVendorMessage, "vendor:"+uuidString(target.ID), string(details)); err != nil {
		log.Printf("server: vendor message audit write failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Flash = "Message sent."
	fail(http.StatusOK, "")
}

// ---- application -----------------------------------------------------------

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
		log.Printf("server: vendor apply lookup failed: %v", sanitizedErr(err))
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
	// One row per user (vendors.user_id UNIQUE): a second submission updates
	// the pending request instead of stacking duplicates behind approval.
	if err := a.auth.VendorCreateRequest(ctx, uid, shop); err != nil {
		log.Printf("server: vendor request write failed: %v", sanitizedErr(err))
		fail("vendor application failed, please try again")
		return
	}
	details, _ := json.Marshal(map[string]any{"shop_name": shop})
	if err := a.auth.AuditAppend(ctx, uid, "user", actionVendorRequest, "vendor:"+uuidString(uid), string(details)); err != nil {
		// Bookkeeping failure must not strand a valid application — loud log,
		// same policy as login history (auth_handlers.go).
		log.Printf("server: vendor request audit write failed (non-fatal): %v", sanitizedErr(err))
	}
	data.Flash = "Application submitted. An administrator will review it."
	a.renderAs(w, "vendor_apply", http.StatusOK, data)
}

// ---- admin approval queue ----------------------------------------------------

func (a *App) handleVendorQueue(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		writeGenericError(w)
		return
	}
	data := a.vendorData(r)
	pending, err := a.auth.VendorListPending(r.Context())
	if err != nil {
		log.Printf("server: pending vendor list failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	data.Pending = pending
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
		a.renderAs(w, "admin_vendors", code, data)
	}

	if honeypotTriggered(r) || !a.requireFriction(w, r, vendorPurpose) {
		fail(http.StatusUnauthorized, "challenge failed, please try again")
		return
	}
	approve := r.FormValue("decision") == "approve"
	vid, err := parseUUIDParam(r, "id")
	if err != nil {
		fail(http.StatusBadRequest, "unknown application")
		return
	}

	// Re-read the authoritative role for the applicant before flipping
	// anything: approving must promote exactly one known 'customer'.
	target, err := a.vendorRowTarget(ctx, vid)
	if err != nil {
		fail(http.StatusBadRequest, "unknown application")
		return
	}
	role, err := a.auth.AuthUserRole(ctx, target.UserID)
	if err != nil {
		log.Printf("server: vendor decision role lookup failed: %v", sanitizedErr(err))
		fail(http.StatusInternalServerError, "approval failed, please try again")
		return
	}
	if role != "customer" && approve {
		fail(http.StatusBadRequest, "unknown application")
		return
	}

	if err := a.auth.VendorSetApproval(ctx, vid, approve); err != nil {
		log.Printf("server: vendor approval write failed: %v", sanitizedErr(err))
		fail(http.StatusInternalServerError, "approval failed, please try again")
		return
	}
	if approve {
		if err := a.auth.VendorSetRoleToVendor(ctx, target.UserID); err != nil {
			log.Printf("server: vendor promotion failed: %v", sanitizedErr(err))
		}
		// Privilege change: kill every live session of the promoted user so
		// no stale 'customer' cookie survives the flip (AGENTS.md §4). The
		// new vendor signs in fresh with the upgraded role.
		if err := a.InvalidateUserSessions(ctx, uuidString(target.UserID)); err != nil {
			log.Printf("server: session invalidation after vendor approval failed: %v", sanitizedErr(err))
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
		log.Printf("server: vendor decision audit write failed (non-fatal): %v", sanitizedErr(err))
	}
	if approve {
		data.Flash = "Vendor approved."
	} else {
		data.Flash = "Application rejected."
	}
	fail(http.StatusOK, "")
}

// vendorRowTarget pulls the vendor row being decided on. VendorStore does not
// expose GetVendorByID (the handlers never needed it elsewhere), so the queue
// template carries the owner/user ids alongside the row id; we reconstruct
// the target from the pending list snapshot instead of trusting the form.
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

// ---- reviews ------------------------------------------------------------------

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
		log.Printf("server: review vendor lookup failed: %v", sanitizedErr(err))
		writeGenericError(w)
		return
	}
	lines, err := a.vendor.CatalogReviewableLines(ctx, uid, prof.ID)
	if err != nil {
		log.Printf("server: reviewable lines failed: %v", sanitizedErr(err))
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
		// Error text here is ours ("store: review window closed") — safe verb,
		// no internals (sanitizedErr still strips driver detail).
		fail(http.StatusBadRequest, "the review window for this order has closed")
		return
	case err != nil:
		log.Printf("server: review submit failed: %v", sanitizedErr(err))
		fail(http.StatusInternalServerError, "review failed, please try again")
		return
	}
	details, _ := json.Marshal(map[string]any{"item_id": itemID, "rating": rating})
	if aerr := a.auth.AuditAppend(ctx, uid, "user", actionReviewSubmit, fmt.Sprintf("order_item:%d", itemID), string(details)); aerr != nil {
		log.Printf("server: review audit write failed (non-fatal): %v", sanitizedErr(aerr))
	}
	data.Flash = "Review saved."
	fail(http.StatusOK, "")
}

// parseUUIDParam reads one chi URL parameter as a UUID, rejecting malformed
// input without echoing it back.
func parseUUIDParam(r *http.Request, key string) (pgtype.UUID, error) {
	raw := chi.URLParam(r, key)
	u := parseUUIDText(raw)
	if !u.Valid {
		return pgtype.UUID{}, errors.New("server: invalid uuid parameter")
	}
	return u, nil
}

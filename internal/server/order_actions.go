package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// escrowGraceDays is the buyer-inspection buffer added to a shipping option's
// eta_days to compute the auto-release deadline.
const escrowGraceDays = 3

func (a *App) registerOrderActionRoutes(r chi.Router) {
	buyer := a.RequireRole("customer", "vendor", "mod", "admin")
	vendor := a.RequireRole("vendor", "admin")
	r.Method(http.MethodPost, "/orders/{id}/ship", vendor(http.HandlerFunc(a.handleOrderShip)))
	r.Method(http.MethodPost, "/orders/{id}/extend", buyer(http.HandlerFunc(a.handleOrderExtend)))
	r.Method(http.MethodPost, "/orders/{id}/release", buyer(http.HandlerFunc(a.handleOrderRelease)))
}

func (a *App) handleOrderShip(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok { writeGenericError(w); return }
	uid := CurrentUserID(r.Context(), a.sm)
	if uid == "" || honeypotTriggered(r) || !a.requireFriction(w, r, "order") {
		http.Redirect(w, r, "/orders", http.StatusSeeOther); return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid { http.Error(w, "bad request", http.StatusBadRequest); return }
	u, _ := parseUUID(uid)

	var vid pgtype.UUID
	var status string
	if err := pool.QueryRow(r.Context(), `SELECT v.user_id, o.status FROM orders o JOIN vendors v ON v.id=o.vendor_id WHERE o.id=$1`, id).Scan(&vid, &status); err != nil {
		http.NotFound(w, r); return
	}
	if uuidString(vid) != uuidString(u) && CurrentRole(r.Context(), a.sm) != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden); return
	}
	if status != "in_escrow" { http.Error(w, "order not in escrow", http.StatusBadRequest); return }

	// Numeric timing from the order's selected shipping option (fallback 14d).
	var etaDays int
	_ = pool.QueryRow(r.Context(), `
		SELECT COALESCE(so.eta_days, 14)
		FROM orders o LEFT JOIN shipping_options so ON so.id = o.shipping_option_id
		WHERE o.id=$1`, id).Scan(&etaDays)
	if etaDays < 1 || etaDays > 60 { etaDays = 14 }
	autoRelease := time.Now().UTC().AddDate(0, 0, etaDays+escrowGraceDays)

	if _, err := pool.Exec(r.Context(), `
		UPDATE orders SET status='shipped', shipped_at=now(), auto_release_at=$1, updated_at=now()
		WHERE id=$2`, autoRelease, id); err != nil { writeGenericError(w); return }

	note := r.FormValue("shipping_note")
	if note == "" { note = "Your order has been shipped." }
	_, _ = pool.Exec(r.Context(), `INSERT INTO order_messages (order_id, sender_type, body) VALUES ($1,'vendor',$2)`, id, note)
	http.Redirect(w, r, "/orders/"+uuidString(id), http.StatusSeeOther)
}

func (a *App) handleOrderExtend(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok { writeGenericError(w); return }
	uid := CurrentUserID(r.Context(), a.sm)
	if uid == "" || honeypotTriggered(r) || !a.requireFriction(w, r, "order") {
		http.Redirect(w, r, "/orders", http.StatusSeeOther); return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid { http.Error(w, "bad request", http.StatusBadRequest); return }
	u, _ := parseUUID(uid)

	var cid pgtype.UUID
	var status string
	var extended bool
	if err := pool.QueryRow(r.Context(), `SELECT customer_id, status, extended FROM orders WHERE id=$1`, id).Scan(&cid, &status, &extended); err != nil {
		http.NotFound(w, r); return
	}
	if uuidString(cid) != uuidString(u) { http.Error(w, "forbidden", http.StatusForbidden); return }
	if status != "shipped" || extended { http.Error(w, "cannot extend", http.StatusBadRequest); return }

	newRelease := time.Now().UTC().AddDate(0, 0, 7)
	if _, err := pool.Exec(r.Context(), `UPDATE orders SET extended=true, auto_release_at=$1, updated_at=now() WHERE id=$2`, newRelease, id); err != nil {
		writeGenericError(w); return
	}
	msg := fmt.Sprintf("Buyer extended escrow by 7 days. New deadline: %s", newRelease.Format("2006-01-02"))
	_, _ = pool.Exec(r.Context(), `INSERT INTO order_messages (order_id, sender_type, body) VALUES ($1,'buyer',$2)`, id, msg)
	http.Redirect(w, r, "/orders/"+uuidString(id), http.StatusSeeOther)
}

func (a *App) handleOrderRelease(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok { writeGenericError(w); return }
	uid := CurrentUserID(r.Context(), a.sm)
	if uid == "" || honeypotTriggered(r) || !a.requireFriction(w, r, "order") {
		http.Redirect(w, r, "/orders", http.StatusSeeOther); return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid { http.Error(w, "bad request", http.StatusBadRequest); return }
	u, _ := parseUUID(uid)

	var cid, vid pgtype.UUID
	var status string
	var pico int64
	if err := pool.QueryRow(r.Context(), `SELECT customer_id, vendor_id, status, xmr_amount_piconero FROM orders WHERE id=$1`, id).Scan(&cid, &vid, &status, &pico); err != nil {
		http.NotFound(w, r); return
	}
	if uuidString(cid) != uuidString(u) { http.Error(w, "forbidden", http.StatusForbidden); return }
	if status != "shipped" { http.Error(w, "order not shipped", http.StatusBadRequest); return }

	var disputeCount int
	_ = pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM disputes WHERE order_id=$1 AND resolution IS NULL`, id).Scan(&disputeCount)
	if disputeCount > 0 { http.Error(w, "cannot release (open dispute)", http.StatusBadRequest); return }

	if _, err := pool.Exec(r.Context(), `UPDATE orders SET status='released', released_at=now(), updated_at=now() WHERE id=$1`, id); err != nil {
		writeGenericError(w); return
	}
	_ = PostEscrowRelease(r.Context(), pool, vid, id, pico)
	msg := fmt.Sprintf("Order #%s finalized early. Funds released to vendor.", uuidString(id)[:8])
	_, _ = pool.Exec(r.Context(), `INSERT INTO order_messages (order_id, sender_type, body) VALUES ($1,'buyer',$2)`, id, msg)
	http.Redirect(w, r, "/orders/"+uuidString(id), http.StatusSeeOther)
}

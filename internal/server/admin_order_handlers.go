package server

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *App) registerAdminOrderRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Get("/admin/orders", a.handleAdminOrders)
	r.With(admin).Post("/admin/orders/{id}/cancel", a.handleAdminForceCancel)
}

type AdminOrderRow struct {
	IDStr     string
	Customer  string
	Vendor    string
	TotalUSD  int64
	Status    string
	CreatedAt string
}

func (a *App) handleAdminOrders(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	data := struct {
		AuthData
		Orders []AdminOrderRow
	}{AuthData: a.baseData(r)}
	rows, err := pool.Query(r.Context(), `
		SELECT o.id, u.username, v.shop_name, o.total_usd_cents, o.status, o.created_at
		FROM orders o
		JOIN users u ON u.id = o.customer_id
		JOIN vendors v ON v.id = o.vendor_id
		ORDER BY o.created_at DESC LIMIT 500`)
	if err != nil {
		log.Printf("server: admin orders list failed: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var o AdminOrderRow
			var id interface{}
			var created interface{}
			if err := rows.Scan(&id, &o.Customer, &o.Vendor, &o.TotalUSD, &o.Status, &created); err == nil {
				o.IDStr = formatUUID(id)
				o.CreatedAt = formatTime(created)
				data.Orders = append(data.Orders, o)
			}
		}
	}
	a.renderAs(w, "admin_orders", http.StatusOK, data)
}

func (a *App) handleAdminForceCancel(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) {
		http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var current string
	if err := pool.QueryRow(r.Context(), `SELECT status FROM orders WHERE id=$1`, id).Scan(&current); err != nil {
		http.NotFound(w, r)
		return
	}
	next, err := Apply(EscrowState(current), EventCancelRequested)
	if err != nil {
		log.Printf("server: force cancel illegal transition: %v", err)
		http.Error(w, "order cannot be cancelled from current state", http.StatusBadRequest)
		return
	}
	if _, err := pool.Exec(r.Context(), `UPDATE orders SET status=$1, cancelled_at=now(), updated_at=now() WHERE id=$2`, string(next), id); err != nil {
		writeGenericError(w)
		return
	}
	_, _ = pool.Exec(r.Context(), `
		INSERT INTO audit_log (actor_id, actor_kind, action, target, details)
		VALUES ($1,'user','order.force_cancel',$2,'admin-initiated cancellation')`, uid, "orders:"+id.String())
	http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
}

package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (a *App) registerAdminDisputeRoutes(r chi.Router) {
	staff := a.RequireRole("mod", "admin")
	r.With(staff).Get("/admin/disputes", a.handleAdminDisputes)
	r.With(staff).Get("/admin/disputes/{id}", a.handleAdminDisputeDetail)
	r.With(staff).Post("/admin/disputes/{id}/release", a.handleAdminDisputeRelease)
	r.With(staff).Post("/admin/disputes/{id}/refund", a.handleAdminDisputeRefund)
}

type DisputeRow struct {
	IDStr     string
	OrderID   string
	OpenedBy  string
	Reason    string
	Status    string
	CreatedAt string
}

func (a *App) handleAdminDisputes(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	data := struct {
		AuthData
		Disputes []DisputeRow
	}{AuthData: a.baseData(r)}
	rows, err := pool.Query(r.Context(), `
		SELECT d.id, d.order_id, u.username, d.reason,
		       CASE WHEN d.resolution IS NULL THEN 'open' ELSE d.resolution END,
		       d.created_at
		FROM disputes d JOIN users u ON u.id = d.opened_by
		ORDER BY d.created_at DESC`)
	if err != nil {
		log.Printf("server: admin disputes list failed: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var d DisputeRow
			var id, oid interface{}
			var created interface{}
			if err := rows.Scan(&id, &oid, &d.OpenedBy, &d.Reason, &d.Status, &created); err != nil {
				continue
			}
			d.IDStr = formatUUID(id)
			d.OrderID = formatUUID(oid)
			d.CreatedAt = formatTime(created)
			data.Disputes = append(data.Disputes, d)
		}
	}
	a.renderAs(w, "admin_disputes", http.StatusOK, data)
}

func (a *App) handleAdminDisputeDetail(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.NotFound(w, r)
		return
	}
	data := struct {
		AuthData
		Dispute     DisputeRow
		Messages    []DisputeMessage
		OrderStatus string
	}{AuthData: a.baseData(r)}
	if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
		return
	}
	var did, oid interface{}
	var created interface{}
	err := pool.QueryRow(r.Context(), `
		SELECT d.id, d.order_id, u.username, d.reason,
		       CASE WHEN d.resolution IS NULL THEN 'open' ELSE d.resolution END,
		       d.created_at
		FROM disputes d JOIN users u ON u.id = d.opened_by
		WHERE d.id=$1`, id).
		Scan(&did, &oid, &data.Dispute.OpenedBy, &data.Dispute.Reason, &data.Dispute.Status, &created)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data.Dispute.IDStr = formatUUID(did)
	data.Dispute.OrderID = formatUUID(oid)
	data.Dispute.CreatedAt = formatTime(created)
	rows, err := pool.Query(r.Context(), `
		SELECT u.username, m.body, m.created_at
		FROM dispute_messages m JOIN users u ON u.id = m.author_id
		WHERE m.dispute_id=$1 ORDER BY m.created_at`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var m DisputeMessage
			var created interface{}
			if err := rows.Scan(&m.Author, &m.Body, &created); err == nil {
				m.CreatedAt = formatTime(created)
				data.Messages = append(data.Messages, m)
			}
		}
	}
	_ = pool.QueryRow(r.Context(), `SELECT status FROM orders WHERE id=$1`, oid).Scan(&data.OrderStatus)
	a.renderAs(w, "admin_dispute_detail", http.StatusOK, data)
}

type DisputeMessage struct {
	Author    string
	Body      string
	CreatedAt string
}

func (a *App) handleAdminDisputeRelease(w http.ResponseWriter, r *http.Request) {
	a.adminDisputeResolve(w, r, EventDisputeResolvedRelease, "released")
}

func (a *App) handleAdminDisputeRefund(w http.ResponseWriter, r *http.Request) {
	a.adminDisputeResolve(w, r, EventDisputeResolvedRefund, "refunded")
}

func (a *App) adminDisputeResolve(w http.ResponseWriter, r *http.Request, event EscrowEvent, resolution string) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/admin/disputes", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
		http.Redirect(w, r, "/admin/disputes", http.StatusSeeOther)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if len(reason) < 5 || len(reason) > 4000 {
		http.Error(w, "reason required (5-4000 chars)", http.StatusBadRequest)
		return
	}
	tx, err := pool.Begin(r.Context())
	if err != nil {
		writeGenericError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var orderID interface{}
	var currentOrder string
	err = tx.QueryRow(r.Context(), `
		SELECT d.order_id, o.status
		FROM disputes d JOIN orders o ON o.id = d.order_id
		WHERE d.id=$1`, id).Scan(&orderID, &currentOrder)
	if err != nil {
		writeGenericError(w)
		return
	}
	next, err := Apply(EscrowState(currentOrder), event)
	if err != nil {
		writeGenericError(w)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE orders SET status=$1, updated_at=now() WHERE id=$2`, string(next), orderID); err != nil {
		writeGenericError(w)
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE disputes SET resolution=$1, resolved_by=$2, resolved_at=now() WHERE id=$3`, resolution, uid, id); err != nil {
		writeGenericError(w)
		return
	}
	_, _ = tx.Exec(r.Context(), `
		INSERT INTO audit_log (actor_id, actor_kind, action, target, details)
		VALUES ($1,'user',$2,$3,$4)`, uid, "dispute.resolve."+resolution, "disputes:"+id.String(), reason)
	if err := tx.Commit(r.Context()); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/admin/disputes", http.StatusSeeOther)
}

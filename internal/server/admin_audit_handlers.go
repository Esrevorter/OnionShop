package server

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *App) registerAdminAuditRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Get("/admin/audit", a.handleAdminAudit)
}

type AuditRow struct {
	Actor     string
	ActorKind string
	Action    string
	Target    string
	Details   string
	CreatedAt string
}

func (a *App) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	data := struct {
		AuthData
		Entries []AuditRow
	}{AuthData: a.baseData(r)}
	rows, err := pool.Query(r.Context(), `
		SELECT COALESCE(u.username,''), a.actor_kind, a.action, a.target, a.details::text, a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
		ORDER BY a.created_at DESC LIMIT 500`)
	if err != nil {
		log.Printf("server: audit list failed: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var e AuditRow
			var created interface{}
			if err := rows.Scan(&e.Actor, &e.ActorKind, &e.Action, &e.Target, &e.Details, &created); err == nil {
				e.CreatedAt = formatTime(created)
				data.Entries = append(data.Entries, e)
			}
		}
	}
	a.renderAs(w, "admin_audit", http.StatusOK, data)
}

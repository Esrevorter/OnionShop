package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *App) registerAdminSessionRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Get("/admin/users/{id}/sessions", a.handleAdminUserSessions)
	r.With(admin).Post("/admin/users/{id}/sessions/kill", a.handleAdminKillSessions)
}

type AdminSessionRow struct {
	ID        string
	ExpiresAt string
	CreatedAt string
	UpdatedAt string
}

func (a *App) handleAdminUserSessions(w http.ResponseWriter, r *http.Request) {
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
		UserID   string
		Username string
		Sessions []AdminSessionRow
	}{AuthData: a.baseData(r)}
	data.UserID = id.String()
	_ = pool.QueryRow(r.Context(), `SELECT username FROM users WHERE id=$1`, id).Scan(&data.Username)
	rows, err := pool.Query(r.Context(), `
		SELECT id, expires_at, created_at, updated_at
		FROM sessions WHERE user_id=$1 ORDER BY updated_at DESC`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s AdminSessionRow
			var exp, created, updated interface{}
			if err := rows.Scan(&s.ID, &exp, &created, &updated); err == nil {
				s.ExpiresAt = formatTime(exp)
				s.CreatedAt = formatTime(created)
				s.UpdatedAt = formatTime(updated)
				data.Sessions = append(data.Sessions, s)
			}
		}
	}
	a.renderAs(w, "admin_user_sessions", http.StatusOK, data)
}

func (a *App) handleAdminKillSessions(w http.ResponseWriter, r *http.Request) {
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if err := a.InvalidateUserSessions(r.Context(), id.String()); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

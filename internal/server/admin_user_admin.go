package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

var adminUsernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

var adminAssignableRoles = map[string]bool{
	"customer": true, "vendor": true, "mod": true, "admin": true,
}

func (a *App) registerAdminUserAdminRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Post("/admin/users/create", a.handleAdminCreateUser)
	r.With(admin).Post("/admin/users/{id}/role", a.handleAdminSetRole)
}

func (a *App) adminUsersFail(w http.ResponseWriter, r *http.Request, msg string) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	data := adminUserListData{AuthData: a.baseData(r)}
	data.Error = msg
	if users, err := fetchAdminUsers(r.Context(), pool); err == nil {
		data.Users = users
	}
	if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
		return
	}
	a.renderAs(w, "admin_users", http.StatusUnprocessableEntity, data)
}

func (a *App) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	if honeypotTriggered(r) {
		a.adminUsersFail(w, r, "Human verification failed, please try again.")
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	role := strings.TrimSpace(r.FormValue("role"))
	shop := strings.TrimSpace(r.FormValue("shop_name"))
	if !adminUsernameRe.MatchString(username) {
		a.adminUsersFail(w, r, "Username must be 3-32 chars: lowercase letters, numbers, underscore.")
		return
	}
	if len(password) < 8 {
		a.adminUsersFail(w, r, "Initial password must be at least 8 characters.")
		return
	}
	if !adminAssignableRoles[role] {
		a.adminUsersFail(w, r, "Unknown role.")
		return
	}
	hash, _, err := HashPassword(password)
	if err != nil {
		writeGenericError(w)
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (username, password_hash, role, status)
		VALUES ($1,$2,$3,'active')`, username, hash, role); err != nil {
		a.adminUsersFail(w, r, "Could not create user (username may already exist).")
		return
	}
	if role == "vendor" {
		if shop == "" {
			shop = username
		}
		_, _ = pool.Exec(ctx, `
			INSERT INTO vendors (user_id, shop_name, approved)
			SELECT id, $2, TRUE FROM users WHERE username = $1
			ON CONFLICT DO NOTHING`, username, shop)
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (a *App) handleAdminSetRole(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	if honeypotTriggered(r) {
		a.adminUsersFail(w, r, "Human verification failed, please try again.")
		return
	}
	uid := parseUUIDText(chi.URLParam(r, "id"))
	if !uid.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	role := strings.TrimSpace(r.FormValue("role"))
	if !adminAssignableRoles[role] {
		a.adminUsersFail(w, r, "Unknown role.")
		return
	}
	var targetRole string
	if err := pool.QueryRow(ctx, `SELECT role FROM users WHERE id=$1`, uid).Scan(&targetRole); err != nil {
		a.adminUsersFail(w, r, "User not found.")
		return
	}
	self := uuidString(uid) == CurrentUserID(ctx, a.sm)
	if self && role != "admin" {
		a.adminUsersFail(w, r, "You cannot remove your own admin role.")
		return
	}
	if targetRole == "admin" && role != "admin" {
		var adminCount int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&adminCount); err == nil {
			if adminCount <= 1 {
				a.adminUsersFail(w, r, "Refusing to demote the last remaining admin.")
				return
			}
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role=$1, updated_at=now() WHERE id=$2`, role, uid); err != nil {
		writeGenericError(w)
		return
	}
	if err := a.InvalidateUserSessions(ctx, uuidString(uid)); err != nil {
		_ = err // non-fatal: sessions die at expiry anyway
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

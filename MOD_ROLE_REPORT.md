=========== admin 'mod' role diagnosis ===========

1) handler source
   file: internal/server/admin_user_admin.go
func (a *App) handleAdminSetRole(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
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

2) template role control
<select id="role" name="role">
        <option value="customer">customer</option>
        <option value="vendor">vendor</option>
        <option value="mod">mod (operator)</option>
        <option value="admin">admin</option>
      </select>

3) database constraint + current role
users_username_check|CHECK ((username ~ '^[a-z0-9_]{3,32}$'::text))
users_role_check|CHECK ((role = ANY (ARRAY['customer'::text, 'vendor'::text, 'mod'::text, 'admin'::text])))
users_status_check|CHECK ((status = ANY (ARRAY['pending'::text, 'active'::text, 'suspended'::text, 'banned'::text])))
users_personal_phrase_check|CHECK (((personal_phrase IS NULL) OR ((char_length(personal_phrase) >= 4) AND (char_length(personal_phrase) <= 64))))
denicoman|admin|active
dekoperman|customer|active
deoperatortest|customer|active
devendortest|vendor|active

4) fixes
   DB CHECK already permits 'mod' (or absent)
   could not locate an allow-list; inspect the printed handler
   template already offers 'mod'
   handler already invalidates sessions

5) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   VERIFIED origin/main == HEAD

Applied:

Manual:
 - Handler role validation not auto-patched — paste handleAdminSetRole.

After deploy: re-login as deoperatortest so the new role is in the session.
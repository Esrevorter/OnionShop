=========== admin friction + role reconciliation ===========

1) evidence

--- handleAdminSetRole (internal/server/admin_user_admin.go) ---
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

--- handleAdminBanUser (internal/server/admin_handlers.go) ---
func (a *App) handleAdminBanUser(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		log.Printf("server: admin ban failed: database pool is not *pgxpool.Pool")
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()

	fail := func(msg string) {
		data := adminUserListData{AuthData: a.baseData(r)}
		data.Error = msg
		users, err := fetchAdminUsers(ctx, pool)
		if err == nil {
			data.Users = users
		}
		if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
			return
		}
		a.renderAs(w, "admin_users", http.StatusUnprocessableEntity, data)
	}

	if !a.requireFriction(w, r, "admin") {
		fail("Human verification failed, please try again.")
		return
	}

	targetID := strings.TrimSpace(r.FormValue("user_id"))
	action := r.FormValue("action")
	if targetID == "" || (action != "ban" && action != "unban") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	uid := parseUUIDText(targetID)
	if !uid.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	newStatus := "active"
	if action == "ban" {
		newStatus = "banned"
	}
	if _, err := pool.Exec(ctx, `UPDATE public.users SET status = $1, updated_at = now() WHERE id = $2`, newStatus, uid); err != nil {
		log.Printf("server: admin ban write failed: %v", err)
		writeGenericError(w)
		return
	}

	if action == "ban" {
		if err := a.InvalidateUserSessions(ctx, targetID); err != nil {
			log.Printf("server: admin ban session kill failed (non-fatal): %v", err)
		}
	}

	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

--- handleAdminResetPassword (internal/server/admin_handlers.go) ---
func (a *App) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)

	userIDStr := chi.URLParam(r, "id")
	uid := parseUUIDText(userIDStr)
	if !uid.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !a.requireFriction(w, r, "admin") {
		data.Error = "Human verification failed."
		users, _ := fetchAdminUsers(ctx, pool)
		a.renderAs(w, "admin_users", http.StatusUnprocessableEntity, adminUserListData{AuthData: data, Users: users})
		return
	}

	tempPass := generateTempPassword()
	hash, _, err := HashPassword(tempPass)
	if err != nil {
		log.Printf("server: password hash failed: %v", err)
		writeGenericError(w)
		return
	}

	_, err = pool.Exec(ctx, `UPDATE public.users SET password_hash = $1, updated_at = now() WHERE id = $2`, hash, uid)
	if err != nil {
		log.Printf("server: password reset update failed: %v", err)
		writeGenericError(w)
		return
	}

	if err := a.InvalidateUserSessions(ctx, uuidString(uid)); err != nil {
		log.Printf("server: session invalidation after password reset failed (non-fatal): %v", err)
	}

	data.Flash = fmt.Sprintf("Password reset successful. Temporary password: %s (Provide this to the user securely)", tempPass)

	users, err := fetchAdminUsers(ctx, pool)
	if err == nil {
		a.renderAs(w, "admin_users", http.StatusOK, adminUserListData{AuthData: data, Users: users})
	} else {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
	}
}

--- handleAdminUsers (internal/server/admin_handlers.go) ---
func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		log.Printf("server: admin users failed: database pool is not *pgxpool.Pool")
		writeGenericError(w)
		return
	}
	data := adminUserListData{AuthData: a.baseData(r)}
	users, err := fetchAdminUsers(r.Context(), pool)
	if err != nil {
		log.Printf("server: admin user list failed: %v", err)
		data.Error = "could not load users"
		a.renderAs(w, "admin_users", http.StatusInternalServerError, data)
		return
	}
	data.Users = users
	if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
		return
	}
	a.renderAs(w, "admin_users", http.StatusOK, data)
}

--- web/templates/page_admin_users.html ---
{{template "layout" .}}
{{define "title"}}Admin · Users · OnionShop{{end}}
{{define "body"}}
<section class="admin-layout">
  <div class="admin-header">
    <h1>User Administration</h1>
    <a href="/admin" class="btn-secondary">← Dashboard</a>
  </div>

  {{if .Error}}<div class="flash" role="alert" style="border-color:var(--danger,#f87171); color:#fca5a5;">{{.Error}}</div>{{end}}

  <div class="auth-card" style="max-width:520px; margin-bottom:2rem;">
    <h2>Create User</h2>
    <form method="post" action="/admin/users/create" autocomplete="off">
      {{template "friction" .}}
      <label for="username">Username (3-32, lowercase letters/numbers/underscore)</label>
      <input id="username" name="username" maxlength="32" required>
      <label for="password">Initial password (min 8 characters)</label>
      <input id="password" type="password" name="password" minlength="8" required>
      <label for="role">Role</label>
      <select id="role" name="role">
        <option value="customer">customer</option>
        <option value="vendor">vendor</option>
        <option value="mod">mod (operator)</option>
        <option value="admin">admin</option>
      </select>
      <label for="shop_name">Shop name (vendors only; defaults to username)</label>
      <input id="shop_name" name="shop_name" maxlength="60">
      <div class="form-actions"><button type="submit">Create User</button></div>
    </form>
    <p class="muted">Vendors are created pre-approved with their shop row. Share the initial password securely; the user should change it after first login.</p>
  </div>

  <div class="table-container">
    <table class="data-table">
      <thead><tr><th>Username</th><th>Role</th><th>Status</th><th>Joined</th><th>Actions</th></tr></thead>
      <tbody>
      {{range .Users}}
      <tr>
        <td><strong>{{.Username}}</strong></td>
        <td><span class="badge badge-success">{{.Role}}</span></td>
        <td><span class="badge {{if eq .Status "active"}}badge-success{{else}}badge-muted{{end}}">{{.Status}}</span></td>
        <td>{{.CreatedAt.Format "2006-01-02"}}</td>
        <td>
          <div style="display:flex; gap:.4rem; flex-wrap:wrap; align-items:center;">
            <form method="post" action="/admin/users/ban" style="display:inline;">
              <input type="hidden" name="csrf_token" value="{{$.CsrfToken}}">
              <input type="hidden" name="user_id" value="{{.ID}}">
              <input type="hidden" name="action" value="{{if eq .Status "banned"}}unban{{else}}ban{{end}}">
              <button type="submit" class="btn-danger btn-sm">{{if eq .Status "banned"}}Unban{{else}}Ban{{end}}</button>
            </form>
            <form method="post" action="/admin/users/{{.ID}}/reset-password" style="display:inline;">
              <input type="hidden" name="csrf_token" value="{{$.CsrfToken}}">
              <button type="submit" class="button secondary btn-sm">Reset PW</button>
            </form>
            <a href="/admin/users/{{.ID}}/sessions" class="btn-secondary btn-sm">Sessions</a>
            <form method="post" action="/admin/users/{{.ID}}/role" style="display:inline-flex; gap:.3rem; align-items:center;">
              <input type="hidden" name="csrf_token" value="{{$.CsrfToken}}">
              <select name="role" aria-label="New role for {{.Username}}">
                <option value="customer">customer</option>
                <option value="vendor">vendor</option>
                <option value="mod">mod</option>
                <option value="admin">admin</option>
              </select>
              <button type="submit" class="button secondary btn-sm">Set Role</button>
            </form>
          </div>
        </td>
      </tr>
      {{end}}
      </tbody>
    </table>
  </div>
</section>
{{end}}


2) friction contract reconciliation (every template, every POST form)
   - friction gate removed from handleTicketClose (CSRF+honeypot per policy)
   + handleProductList mints 'account' challenge
   + handleWallet mints 'account' challenge

3) 'mod' role path

4) duplicate error banners (layout already renders .Error)

5) CAPTCHA placed inside the form it guards

6) verification: no form may verify a challenge it does not carry
   mismatches remaining: 0

7) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   OK   docker compose config --quiet
   VERIFIED origin/main == HEAD

Applied:
 - handleTicketClose no longer requires an uncarried CAPTCHA
 - handleProductList mints the challenge its forms verify
 - handleWallet mints the challenge its forms verify

Manual:
 - Last-admin guard not auto-inserted; anchors undetected in handleAdminSetRole.

Re-login as deoperatortest after the role change so the session carries 'mod'.
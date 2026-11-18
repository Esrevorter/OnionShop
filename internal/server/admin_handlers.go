package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (a *App) adminPool() (*pgxpool.Pool, bool) {
	p, ok := a.deps.Pool.(*pgxpool.Pool)
	return p, ok
}

type AdminDashboardData struct {
	AuthData
	UserCount      int64
	VendorCount    int64
	PendingVendors int64
	OrderCount     int64
	DisputeCount   int64
}

type AdminUserData struct {
	ID        string
	Username  string
	Role      string
	Status    string
	CreatedAt time.Time
}

type adminUserListData struct {
	AuthData
	Users []AdminUserData
}

type AdminCategory struct {
	ID   int64
	Slug string
	Name string
}

type CategoryData struct {
	AuthData
	Categories []AdminCategory
}

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		log.Printf("server: admin dashboard failed: database pool is not *pgxpool.Pool (type: %T)", a.deps.Pool)
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	data := &AdminDashboardData{AuthData: a.baseData(r)}

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.users`).Scan(&data.UserCount); err != nil {
		log.Printf("server: admin user count failed: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.vendors`).Scan(&data.VendorCount); err != nil {
		log.Printf("server: admin vendor count failed: %v", err)
	}
	if a.auth != nil {
		if pending, err := a.auth.VendorListPending(ctx); err == nil {
			data.PendingVendors = int64(len(pending))
		}
	}
	data.OrderCount, data.DisputeCount = 0, 0

	a.renderAs(w, "admin_dashboard", http.StatusOK, data)
}

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
	a.renderAs(w, "admin_users", http.StatusOK, data)
}

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

func fetchAdminUsers(ctx context.Context, pool *pgxpool.Pool) ([]AdminUserData, error) {
	rows, err := pool.Query(ctx, `SELECT id, username, role, status, created_at FROM public.users ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminUserData{}
	for rows.Next() {
		var (
			raw pgtype.UUID
			u   AdminUserData
		)
		if err := rows.Scan(&raw, &u.Username, &u.Role, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.ID = uuidString(raw)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (a *App) handleAdminCategories(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	data := a.baseData(r)

	rows, err := pool.Query(ctx, `SELECT id, slug, name FROM public.categories ORDER BY id ASC`)
	if err != nil {
		log.Printf("server: admin category list failed: %v", err)
		data.Error = "could not load categories"
		a.renderAs(w, "admin_categories", http.StatusInternalServerError, CategoryData{AuthData: data})
		return
	}
	defer rows.Close()

	var categories []AdminCategory
	for rows.Next() {
		var c AdminCategory
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name); err != nil {
			log.Printf("server: admin category scan failed: %v", err)
			continue
		}
		categories = append(categories, c)
	}

	a.renderAs(w, "admin_categories", http.StatusOK, CategoryData{AuthData: data, Categories: categories})
}

func (a *App) handleAdminCategoryCreate(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)

	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
			return
		}
		rows, err := pool.Query(ctx, `SELECT id, slug, name FROM public.categories ORDER BY id ASC`)
		if err == nil {
			defer rows.Close()
			var categories []AdminCategory
			for rows.Next() {
				var c AdminCategory
				if err := rows.Scan(&c.ID, &c.Slug, &c.Name); err == nil {
					categories = append(categories, c)
				}
			}
			a.renderAs(w, "admin_categories", http.StatusUnprocessableEntity, CategoryData{AuthData: data, Categories: categories})
			return
		}
		a.renderAs(w, "admin_categories", http.StatusUnprocessableEntity, CategoryData{AuthData: data})
	}

	if !a.requireFriction(w, r, "admin") {
		fail("Human verification failed, please try again.")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.ToLower(strings.TrimSpace(r.FormValue("slug")))

	if len(name) < 2 || len(name) > 50 {
		fail("Category name must be 2-50 characters.")
		return
	}
	if len(slug) < 2 || len(slug) > 50 {
		fail("Category slug must be 2-50 characters.")
		return
	}
	for _, c := range slug {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			fail("Slug can only contain lowercase letters, numbers, and hyphens.")
			return
		}
	}

	_, err := pool.Exec(ctx, `INSERT INTO public.categories (slug, name) VALUES ($1, $2)`, slug, name)
	if err != nil {
		log.Printf("server: admin category create failed: %v", err)
		fail("Could not create category. Slug may already exist.")
		return
	}

	data.Flash = "Category created successfully."
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func generateTempPassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[num.Int64()]
	}
	return string(b)
}

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

func (a *App) registerAdminRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Get("/admin", a.handleAdminDashboard)
	r.With(admin).Get("/admin/users", a.handleAdminUsers)
	r.With(admin).Post("/admin/users/ban", a.handleAdminBanUser)
	r.With(admin).Post("/admin/users/{id}/reset-password", a.handleAdminResetPassword)
	r.With(admin).Get("/admin/categories", a.handleAdminCategories)
	r.With(admin).Post("/admin/categories", a.handleAdminCategoryCreate)
}

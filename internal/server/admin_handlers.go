package server

import (
	"context"
	"log"
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

	// fail closure re-renders the user list with an error and a FRESH CAPTCHA
	fail := func(msg string) {
		data := adminUserListData{AuthData: a.baseData(r)}
		data.Error = msg
		users, err := fetchAdminUsers(ctx, pool)
		if err == nil {
			data.Users = users
		}
		if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
			return // newChallenge already wrote the generic 500
		}
		a.renderAs(w, "admin_users", http.StatusUnprocessableEntity, data)
	}

	// ENFORCE CAPTCHA + HONEYPOT
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

func (a *App) registerAdminRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Get("/admin", a.handleAdminDashboard)
	r.With(admin).Get("/admin/users", a.handleAdminUsers)
	r.With(admin).Post("/admin/users/ban", a.handleAdminBanUser)
}

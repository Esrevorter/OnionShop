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

// Phase A admin surface (UI.md §5.A): dashboard overview + user management.
// These handlers run arbitrary read queries against the live pool, so they
// type-assert Deps.Pool to the concrete *pgxpool.Pool that main.go wires in.
// The narrow interface (Close/Ping) is preserved for the health probe and the
// test fakes; admin is production-only (CI has no Postgres), and the assertion
// fails closed to a generic 500 in any environment that lacks the real pool.

// adminPool returns the concrete pgx pool behind the narrow Deps.Pool
// interface, or ok=false when the app was wired with a non-pool fake (tests).
func (a *App) adminPool() (*pgxpool.Pool, bool) {
	p, ok := a.deps.Pool.(*pgxpool.Pool)
	return p, ok
}

// AdminDashboardData backs page_admin_dashboard: headline counts plus the
// pending-vendor queue length so the operator sees work-in-progress at a glance.
type AdminDashboardData struct {
	AuthData
	UserCount      int64
	VendorCount    int64
	PendingVendors int64
	OrderCount     int64
	DisputeCount   int64
}

// AdminUserData is one row of the user-management table. ID is the canonical
// UUID text (not pgtype.UUID) so templates can emit it into a hidden form
// field without a Stringer dependency.
type AdminUserData struct {
	ID        string
	Username  string
	Role      string
	Status    string
	CreatedAt time.Time
}

// adminUserListData wraps AuthData with the user slice for renderAs.
type adminUserListData struct {
	AuthData
	Users []AdminUserData
}

// handleAdminDashboard renders the Phase A.1 overview cards.
func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	data := &AdminDashboardData{AuthData: a.baseData(r)}

	// Headline counts. Orders/disputes tables may not be populated yet; a
	// failed count is non-fatal and simply leaves the card at zero.
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

// handleAdminUsers renders the Phase A.2 user table.
func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
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

// handleAdminBanUser toggles a user between active and banned, then kills
// their live sessions so a ban takes effect immediately (AGENTS.md §4:
// privilege/state changes must never ride an old cookie).
func (a *App) handleAdminBanUser(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()

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

	// Kill every live session of the affected user so a ban is effective now,
	// not at next login. Nil invalidator (dev in-memory store) is a no-op.
	if action == "ban" {
		if err := a.InvalidateUserSessions(ctx, targetID); err != nil {
			log.Printf("server: admin ban session kill failed (non-fatal): %v", err)
		}
	}

	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// fetchAdminUsers pulls the user table for the admin view. Read-only, newest
// first; bounded so a huge table cannot OOM the page (pagination is a later
// polish item, not a correctness gate for Phase A).
func fetchAdminUsers(ctx context.Context, pool *pgxpool.Pool) ([]AdminUserData, error) {
	rows, err := pool.Query(ctx, `SELECT id, username, role, status, created_at
FROM public.users ORDER BY created_at DESC LIMIT 500`)
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

// registerAdminRoutes mounts the Phase A admin surface. Called from newApp
// AFTER the middleware stack exists (sessions/CSRF/rate-limit), so every
// route is gated by RequireRole("admin") and CSRF-protected on POST.
func (a *App) registerAdminRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.Get("/admin", admin(http.HandlerFunc(a.handleAdminDashboard)))
	r.Get("/admin/users", admin(http.HandlerFunc(a.handleAdminUsers)))
	r.Post("/admin/users/ban", admin(http.HandlerFunc(a.handleAdminBanUser)))
}

package server

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type PendingVendor struct {
	VendorID  string
	UserID    string
	ShopName  string
	Username  string
	CreatedAt string
}

func (a *App) registerAdminVendorQueueRoutes(r chi.Router) {
	admin := a.RequireRole("admin")
	r.With(admin).Get("/admin/vendors", a.handleAdminVendors)
	r.With(admin).Post("/admin/vendors/{id}/approve", a.handleAdminApproveVendor)
	r.With(admin).Post("/admin/vendors/{id}/reject", a.handleAdminRejectVendor)
}

func (a *App) handleAdminVendors(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	data := struct {
		AuthData
		Pending []PendingVendor
	}{AuthData: a.baseData(r)}
	if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
		return
	}
	rows, err := pool.Query(r.Context(), `
		SELECT v.id, v.user_id, v.shop_name, u.username, v.created_at
		FROM vendors v JOIN users u ON u.id = v.user_id
		WHERE v.approved = FALSE ORDER BY v.created_at`)
	if err != nil {
		log.Printf("server: pending vendors query failed: %v", err)
		data.Error = "Could not load pending applications"
		a.renderAs(w, "admin_vendors", http.StatusOK, data)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p PendingVendor
		// (vid/uid types inferred from scan)
		var created interface{}
		// use generic scan since we're joining two tables
		var vidRaw, uidRaw interface{}
		if err := rows.Scan(&vidRaw, &uidRaw, &p.ShopName, &p.Username, &created); err != nil {
			continue
		}
		// convert to strings via format
		p.VendorID = formatUUID(vidRaw) // vidRaw, uidRaw from scan
		p.UserID = formatUUID(uidRaw)
		p.CreatedAt = formatTime(created)
		data.Pending = append(data.Pending, p)
	}
	a.renderAs(w, "admin_vendors", http.StatusOK, data)
}

func formatUUID(v interface{}) string {
	if v == nil {
		return ""
	}
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String()
	}
	return ""
}

func formatTime(v interface{}) string {
	if v == nil {
		return ""
	}
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String()
	}
	return ""
}

func (a *App) handleAdminApproveVendor(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
		http.Redirect(w, r, "/admin/vendors", http.StatusSeeOther)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_, err := pool.Exec(r.Context(), `UPDATE vendors SET approved=TRUE WHERE id=$1`, id)
	if err != nil {
		log.Printf("server: approve vendor failed: %v", err)
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/admin/vendors", http.StatusSeeOther)
}

func (a *App) handleAdminRejectVendor(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
		http.Redirect(w, r, "/admin/vendors", http.StatusSeeOther)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_, err := pool.Exec(r.Context(), `DELETE FROM vendors WHERE id=$1`, id)
	if err != nil {
		log.Printf("server: reject vendor failed: %v", err)
	}
	http.Redirect(w, r, "/admin/vendors", http.StatusSeeOther)
}

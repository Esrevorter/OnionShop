// RBAC middleware for the four OnionShop roles (customer, vendor, mod, admin)
// plus account tiers (basic, upgraded). See ARCHITECTURE.md §3.
//
// Trust model: the role is copied into the session at login by RotateAndLogin
// from the DB-authoritative value only. Handlers that WRITE privilege changes
// (admin role edits, tier upgrades) must call RotateAndLogin again — a role
// change without rotation is a bug per AGENTS.md §4 ("rotation on privilege
// change").

package server

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
)

// Role values mirror the users.role CHECK constraint exactly; anything else
// fails closed.
const (
	RoleCustomer = "customer"
	RoleVendor   = "vendor"
	RoleMod      = "mod"
	RoleAdmin    = "admin"
)

// Tier values mirror users.tier (migration 0006).
const (
	TierBasic    = "basic"
	TierUpgraded = "upgraded"
)

var validRoles = map[string]bool{RoleCustomer: true, RoleVendor: true, RoleMod: true, RoleAdmin: true}

// RequireRole gates a route on an exact role match. Anonymous sessions get the
// uniform 403 (SECURITY_AND_OPSEC.md §1: no signal distinguishing "not logged
// in" from "not allowed" to off-site observers; the onion UI layer can prompt
// for login based on its own state, not this status code).
func RequireRole(sm *scs.SessionManager, want string) func(http.Handler) http.Handler {
	return requireAnyRole(sm, []string{want})
}

// RequireAnyRole admits sessions holding one of the listed roles. Mod and
// Admin are NOT implicit supersets here: every staff surface enumerates its
// roles explicitly so widening someone's access is always a visible diff.
func RequireAnyRole(sm *scs.SessionManager, want ...string) func(http.Handler) http.Handler {
	return requireAnyRole(sm, want)
}

func requireAnyRole(sm *scs.SessionManager, want []string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(want))
	for _, w := range want {
		if !validRoles[w] {
			panic("server: RequireRole called with unknown role " + w) // programmer error, caught at boot/test
		}
		set[w] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := CurrentRole(r.Context(), sm)
			if got == "" || !set[got] {
				denyForbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireStaff admits mod and admin only. Kept as a named helper because staff
// surfaces appear repeatedly and each should read intentionally.
func RequireStaff(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return RequireAnyRole(sm, RoleMod, RoleAdmin)
}

// RequireTier gates on account tier (e.g. PGP-dependent vendor actions demand
// "upgraded"). The tier is stamped by handlers after SetUserTier + rotation.
func RequireTier(sm *scs.SessionManager, want string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if CurrentUserID(r.Context(), sm) == "" || CurrentTier(r.Context(), sm) != want {
				denyForbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func denyForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("403 forbidden\n"))
}

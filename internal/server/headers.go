// Package server wires the OnionShop HTTP layer: the chi router, security
// headers, session management (scs over the Redis+Postgres store in
// internal/store), CSRF protection and Redis-backed rate limiting.
//
// Non-negotiable constraints (AGENTS.md §1/§4, SECURITY_AND_OPSEC.md §1):
//   - no JavaScript anywhere: CSP keeps `script-src 'none'`;
//   - never log or key anything on the client IP (all traffic is 127.0.0.1);
//   - no browser-facing HSTS: authenticity rides SPKI pinning, not HSTS;
//   - bind only to the UNIX socket / loopback (enforced in cmd/onionshop).
package server

import "net/http"

// CSP is the single source of truth for the Content-Security-Policy, matching
// ARCHITECTURE.md §3 verbatim. nginx re-emits an equivalent policy
// (belt-and-suspenders, SECURITY_AND_OPSEC.md §5.1); if you change one, change
// both — CI's header test guards this exact string.
const CSP = "default-src 'self'; script-src 'none'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; " +
	"upgrade-insecure-requests"

// PermissionsPolicy disables every powerful feature we never use; cheap
// defence against a future dependency sneaking in a capability request.
const PermissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), " +
	"usb=(), magnetometer=(), gyroscope=(), accelerometer=()"

// SecurityHeaders returns middleware applying the ARCHITECTURE.md §3 set to
// every response, including error pages written by later handlers. It
// deliberately does NOT emit Strict-Transport-Security (self-signed onion cert
// + HSTS = permanent lockout footgun) and does NOT touch X-Powered-By-style
// headers because Go sets none.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", CSP)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy", PermissionsPolicy)
			// The onion serves one user-visible page set; nothing here is safe
			// to cache in a shared context, and Tor Browser must not blend
			// responses across sessions.
			h.Set("Cache-Control", "no-store")
			h.Set("Pragma", "no-cache")
			next.ServeHTTP(w, r)
		})
	}
}

// StripForwardedHeaders defends against a client (or a misbehaving upstream)
// injecting forwarding/identity headers that later components trust. Per
// SECURITY_AND_OPSEC.md there is no legitimate proxy chain beyond nginx, which
// already strips these; deleting them again at the app layer is free insurance.
func StripForwardedHeaders() func(http.Handler) http.Handler {
	drop := []string{
		"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
		"X-Real-Ip", "X-Client-Ip", "True-Client-Ip", "Forwarded",
		"X-Original-Url", "X-Rewrite-Url", "Front-End-Https",
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, name := range drop {
				r.Header.Del(name)
			}
			next.ServeHTTP(w, r)
		})
	}
}

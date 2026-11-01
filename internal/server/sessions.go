package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

// Session / CSRF names. Kept in one place so handlers and templates cannot
// drift: AGENTS.md §3 mandates the hidden input be named exactly `csrf_token`.
const (
	SessionCookieName = "session"
	CSRFKey           = "csrf_token" // scs session key holding the double-submit token
	CSRFFormField     = "csrf_token" // form field name in every POST form
	csrfTokenBytes    = 32           // 256-bit tokens (>=128 bits required, AGENTS.md §4)

	createdKey = "sess.created" // absolute-lifetime start, UTC epoch seconds
	userIDKey  = "user_id"      // set by Phase 3 login handlers after rotation
)

// ErrInternalSession marks CSPRNG/store failures. Handlers render a generic 500
// and never surface the cause (SECURITY_AND_OPSEC.md §1: no stack traces).
var ErrInternalSession = errors.New("server: session subsystem failure")

// SessionConfig controls cookie flags and both expiry kinds.
type SessionConfig struct {
	IdleTimeout     time.Duration // sliding window, refreshed on each request
	AbsoluteTimeout time.Duration // hard cap from creation; never extended
	SecureCookies   bool          // false ONLY for local dev (ONIONSHOP_SECURE_COOKIES=false)
	CookieDomain    string        // empty => host-only cookie (correct for an onion)
}

// DefaultSessionConfig returns production defaults: 30 min idle, 12 h absolute.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		IdleTimeout:     30 * time.Minute,
		AbsoluteTimeout: 12 * time.Hour,
		SecureCookies:   true,
	}
}

// NewSessions builds the scs manager over an already-composed store (see
// store.FallbackStore: Redis fast path + Postgres durable mirror).
//
// Cookie flags follow AGENTS.md §4 exactly: HttpOnly + Secure +
// SameSite=Strict, host-only, Path "/". Session identifiers come from scs'
// own generator — 32 bytes of crypto/rand rendered base64url — which meets
// the ≥128-bit requirement with margin; nothing here weakens it.
//
// Expiry: scs v2.8 offers a fixed Lifetime OR a sliding IdleTimeout, not both,
// so we set Lifetime as the store-side upper bound and enforce the real
// absolute cap with the AbsoluteExpiry middleware below.
func NewSessions(st scs.Store, cfg SessionConfig) *scs.SessionManager {
	sm := scs.New()
	sm.Store = st
	sm.Cookie.Name = SessionCookieName
	sm.Cookie.HttpOnly = true
	sm.Cookie.Secure = cfg.SecureCookies
	sm.Cookie.SameSite = http.SameSiteStrictMode
	sm.Cookie.Path = "/"
	sm.Cookie.Persist = true
	if cfg.CookieDomain != "" {
		sm.Cookie.Domain = cfg.CookieDomain
	}
	sm.Lifetime = cfg.AbsoluteTimeout
	sm.IdleTimeout = cfg.IdleTimeout
	// scs' default ErrorFunc log.Printf's the raw error (which can carry the
	// token). Replace it: PII-free generic 500 only (AGENTS.md §4).
	sm.ErrorFunc = func(w http.ResponseWriter, r *http.Request, _ error) {
		writeGenericError(w)
	}
	return sm
}

// AbsoluteExpiry enforces the hard lifetime cap that scs cannot express next
// to a sliding window. The first request of a session stamps createdKey; once
// the stamp is older than timeout the session is destroyed and a fresh token
// issued, so the visitor silently continues anonymously — attackers probing
// stolen tokens get no signal about why.
func AbsoluteExpiry(sm *scs.SessionManager, timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			created := sm.GetInt64(ctx, createdKey)
			switch {
			case created == 0:
				sm.Put(ctx, createdKey, time.Now().UTC().Unix())
			case time.Since(time.Unix(created, 0)) > timeout:
				if err := sm.Destroy(ctx); err != nil {
					writeGenericError(w)
					return
				}
				if err := sm.RenewToken(ctx); err != nil {
					writeGenericError(w)
					return
				}
				sm.Put(ctx, createdKey, time.Now().UTC().Unix())
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RotateOnAuth MUST be called by login and privilege-change handlers
// (AGENTS.md §4). It keeps session data, swaps the identifier, and re-stamps
// nothing — the absolute clock keeps running across the rotation.
func RotateOnAuth(ctx context.Context, sm *scs.SessionManager) error {
	if err := sm.RenewToken(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrInternalSession, err)
	}
	return nil
}

// CurrentUserID returns the authenticated user id stored in the session by
// Phase 3 auth handlers ("" when anonymous). Rate limiting keys off this.
func CurrentUserID(ctx context.Context, sm *scs.SessionManager) string {
	return sm.GetString(ctx, userIDKey)
}

// NewRandomToken returns n cryptographically-random bytes (default 32 => 256
// bits) base64url-encoded. Used for CSRF and later CAPTCHA tokens.
func NewRandomToken(n int) (string, error) {
	if n <= 0 {
		n = csrfTokenBytes
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInternalSession, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken folds a token into a fixed-width digest before comparison so any
// timing channel is bounded and identical for every input length.
func hashToken(t string) [sha256.Size]byte { return sha256.Sum256([]byte(t)) }

// ErrCSRFMismatch is the internal marker for failed validation; clients only
// ever see the same uniform 403 whether the token was missing or wrong.
var ErrCSRFMismatch = errors.New("server: csrf validation failed")

// CSRFLoad guarantees the session carries a double-submit token, minting one
// on first sight. Mount after sessions.LoadAndSave and before CSRFProtect.
func CSRFLoad(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := sm.Get(r.Context(), CSRFKey).(string)
			if !ok || len(tok) < 16 {
				var err error
				if tok, err = NewRandomToken(csrfTokenBytes); err != nil {
					writeGenericError(w)
					return
				}
				sm.Put(r.Context(), CSRFKey, tok)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRFToken returns the live double-submit token for template rendering:
//
//	<input type="hidden" name="csrf_token" value="{{.CsrfToken}}">
func CSRFToken(ctx context.Context, sm *scs.SessionManager) string {
	tok, _ := sm.Get(ctx, CSRFKey).(string)
	return tok
}

// isSafeMethod lists the methods RFC 7231 deems safe/idempotent; they never
// need CSRF validation. Everything else (POST/PUT/PATCH/DELETE) does.
func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// CSRFProtect enforces double-submit validation on state-changing requests:
// the `csrf_token` form field (or X-CSRF-Token header, reserved for the future
// SPKI-pinned CLI) must equal the session-stored token. Comparison runs over
// SHA-256 digests in constant time; failures get a uniform 403 with no detail.
//
// A session without a token (destroyed mid-flow) fails closed.
func CSRFProtect(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			want, _ := sm.Get(r.Context(), CSRFKey).(string)
			got := r.FormValue(CSRFFormField)
			if got == "" {
				got = r.Header.Get("X-CSRF-Token")
			}
			wantHash := hashToken(want)
			gotHash := hashToken(got)
			if want == "" || subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) != 1 {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("403 forbidden\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeGenericError renders the uniform error body mandated by
// SECURITY_AND_OPSEC.md §1 (no stack traces, no internals). Templates take
// over this hook in Phase 3; keeping it here means every middleware that must
// abort has one safe exit.
func writeGenericError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("internal error\n"))
}

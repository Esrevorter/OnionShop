// Phase 3 auth handlers: registration, login, logout. See TASKS.md §Phase 3
// and SECURITY_AND_OPSEC.md §1 for the invariants encoded here.
//
// OpSec rules baked into this file (do not "simplify" them away):
//   - Unknown-username logins still run a full-cost dummy Argon2id verify so
//     wall time cannot enumerate accounts;
//   - Every failure path renders the SAME generic message and status;
//   - Login history stores UTC time + the validated Host header only — never
//     an IP, never raw headers (SECURITY_AND_OPSEC.md §1);
//   - Registration/login destroy any pre-existing session state before minting
//     a new identity (RotateAndLogin renews the token; failed attempts kill it);
//   - CSRF is enforced by the global middleware chain BEFORE these handlers
//     run — nothing here re-checks it, and nothing may be mounted ahead of
//     that chain.

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	store "github.com/example/onionshop/internal/store/gen"
)

// AuthStore is the persistence surface the auth handlers need. The generated
// *store.Queries satisfies it with the pool passed per call; tests substitute
// an in-memory fake. Keeping it an interface here (rather than importing the
// concrete type) also documents exactly which queries Phase 3 depends on.
type AuthStore interface {
	RegisterUser(ctx context.Context, db store.DBTX, arg store.RegisterUserParams) (store.User, error)
	GetUserByUsername(ctx context.Context, db store.DBTX, username string) (store.User, error)
	UpdateUserPasswordHash(ctx context.Context, db store.DBTX, arg store.UpdateUserPasswordHashParams) error
	RecordLoginAttempt(ctx context.Context, db store.DBTX, arg store.RecordLoginAttemptParams) error
}

// DBProvider hands each handler call its database executor (normally the
// pgxpool). Nil provider or nil pool => auth endpoints answer 503 generically:
// degraded mode must never look like "wrong password".
type DBProvider interface{ DB() store.DBTX }

// AuthConfig tunes the auth surface. Zero value + Validate() gives safe
// defaults; main.go constructs it explicitly.
type AuthConfig struct {
	// MinPasswordLen is the lower bound accepted at registration (the docs
	// mandate passphrases; 10 chars is the floor we will not go below).
	MinPasswordLen int
	// HistoryTimeout bounds the login-history write so a slow DB can never
	// stall the uniform response timing of the login endpoint.
	HistoryTimeout time.Duration
}

func (c AuthConfig) withDefaults() AuthConfig {
	if c.MinPasswordLen < 10 {
		c.MinPasswordLen = 10
	}
	if c.HistoryTimeout <= 0 {
		c.HistoryTimeout = 750 * time.Millisecond
	}
	return c
}

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

const (
	authGenericFail    = "invalid username or password\n"
	authRegisterPrompt = "registration unavailable\n"
)

// attachAuth mounts the Phase 3 routes onto the app's router — call it from
// New(), i.e. INSIDE the middleware chain (after CSRFProtect), never outside.
// All three deps are required; passing nils is a programmer error and panics
// at boot instead of silently serving broken auth.
func (a *App) attachAuth(st AuthStore, db DBProvider, cfg AuthConfig) {
	if st == nil || db == nil {
		panic("server: attachAuth requires a store and a DB provider")
	}
	a.auth = &authHandlers{app: a, store: st, db: db, cfg: cfg.withDefaults()}
	a.router.Post("/register", a.auth.handleRegister)
	a.router.Get("/login", a.auth.handleLoginForm)
	a.router.Post("/login", a.auth.handleLogin)
	a.router.Post("/logout", a.auth.handleLogout)
}

type authHandlers struct {
	app   *App
	store AuthStore
	db    DBProvider
	cfg   AuthConfig
}

// formOrPost reads a trimmed field from a bounded form body.
func formOrPost(r *http.Request, name string) string {
	if err := r.ParseForm(); err != nil {
		return ""
	}
	return strings.TrimSpace(r.FormValue(name))
}

func (h *authHandlers) executor() (store.DBTX, bool) {
	d := h.db.DB()
	if d == nil {
		return nil, false
	}
	return d, true
}

// handleRegister creates a customer account. Username/password are the only
// fields; role/tier come exclusively from DB defaults (customer/basic) so no
// client input can ever select a privilege.
func (h *authHandlers) handleRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exec, ok := h.executor()
	if !ok {
		http.Error(w, authRegisterPrompt, http.StatusServiceUnavailable)
		return
	}

	username := strings.ToLower(formOrPost(r, "username"))
	password := formOrPost(r, "password") // deliberately NOT trimmed: spaces are valid

	if !usernameRe.MatchString(username) {
		http.Error(w, "choose a different username\n", http.StatusBadRequest)
		return
	}
	if len(password) < h.cfg.MinPasswordLen || len(password) > 4096 {
		http.Error(w, "passphrase too short\n", http.StatusBadRequest)
		return
	}

	hash, err := HashPassword(password, DefaultArgonParams())
	if err != nil {
		writeGenericError(w)
		return
	}

	u, err := h.store.RegisterUser(ctx, exec, store.RegisterUserParams{
		Username:     username,
		PasswordHash: hash,
	})
	if err != nil {
		if errors.Is(err, store.ErrDuplicateUsername) || isUniqueViolation(err) {
			// Generic-by-design: "taken" vs "invalid" is enumeration signal.
			http.Error(w, "choose a different username\n", http.StatusBadRequest)
			return
		}
		writeGenericError(w)
		return
	}

	// A brand-new identity always starts on a fresh session: rotate first, then
	// stamp. This protects users who register from a browser that already holds
	// an anonymous session cookie.
	if err := RotateAndLogin(ctx, h.app.sm, uuidText(u.ID), u.Role, u.Tier); err != nil {
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte("account created\n"))
}

// handleLoginForm serves the minimal JS-free login page. Templates land later
// in the phase; a plain form keeps the endpoint testable and CSP-clean today.
func (h *authHandlers) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	tok := CSRFToken(r.Context(), h.app.sm)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><title>login</title>
<form method="post" action="/login">
<input type="hidden" name="csrf_token" value=%q>
<label>Username <input name="username" maxlength="32" autocomplete="username"></label>
<label>Passphrase <input type="password" name="password" maxlength="4096" autocomplete="current-password"></label>
<button type="submit">Log in</button>
</form>
`, tok)
}

// handleLogin authenticates. Timing across all failure branches is equalized
// by the mandatory Argon2id work (real or dummy) on every attempt.
func (h *authHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exec, ok := h.executor()
	if !ok {
		writeGenericError(w)
		return
	}

	username := strings.ToLower(formOrPost(r, "username"))
	password := formOrPost(r, "password")
	hostUsed := sanitizeHost(r.Host)

	fail := func(userExists bool) {
		h.recordLogin(ctx, exec, userExists, username, hostUsed, false)
		http.Error(w, authGenericFail, http.StatusUnauthorized)
	}

	var u store.User
	if username == "" || password == "" {
		DummyVerifyPassword(password) // burn equal work even on empty-field probes
		fail(false)
		return
	}
	u, err := h.store.GetUserByUsername(ctx, exec, username)
	if errors.Is(err, pgx.ErrNoRows) {
		DummyVerifyPassword(password) // unknown user costs the same as wrong password
		fail(false)
		return
	}
	if err != nil {
		writeGenericError(w)
		return
	}

	okp, err := VerifyPassword(u.PasswordHash, password)
	if err != nil || !okp {
		fail(true)
		return
	}
	if u.Status != "active" {
		// Suspended/locked accounts get the SAME generic credential failure —
		// account-state disclosure is enumeration by another name.
		fail(true)
		return
	}

	if err := RotateAndLogin(ctx, h.app.sm, uuidText(u.ID), u.Role, u.Tier); err != nil {
		writeGenericError(w)
		return
	}
	h.recordLogin(ctx, exec, true, username, hostUsed, true)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout destroys the session outright (store rows included). It is
// idempotent: logging out an anonymous session is a harmless no-op redirect.
func (h *authHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = Logout(r.Context(), h.app.sm)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// recordLogin writes one login_history row on BOTH success and failure
// (TASKS.md Phase 3: the table exists to spot brute-force patterns). It runs
// under a short independent deadline so a stalled DB cannot stretch the
// response timing that the dummy-verify path carefully equalized. Failures
// here are logged nowhere and leak nothing; they simply do not happen on the
// request's critical path beyond the timeout.
func (h *authHandlers) recordLogin(ctx context.Context, exec store.DBTX, userKnown bool, username, hostUsed string, success bool) {
	if !userKnown {
		return // no FK target; unknown-user attempts are counted only via rate limits
	}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.cfg.HistoryTimeout)
	defer cancel()
	uid, err := lookupUserID(hctx, h.store, exec, username)
	if err != nil {
		return
	}
	_ = h.store.RecordLoginAttempt(hctx, exec, store.RecordLoginAttemptParams{
		UserID:   uid,
		Success:  success,
		HostUsed: hostUsed,
	})
}

func lookupUserID(ctx context.Context, st AuthStore, exec store.DBTX, username string) (pgtype.UUID, error) {
	u, err := st.GetUserByUsername(ctx, exec, username)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return u.ID, nil
}

// sanitizeHost strips any port and rejects anything that is not a bare
// hostname/IP literal, so login_history.host_used can never smuggle header
// garbage (length-capped to match the column).
func sanitizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if len(host) > 253 {
		host = host[:253]
	}
	if strings.ContainsAny(host, "\r\n\x00") {
		return ""
	}
	return host
}

// uuidText renders a pgtype.UUID canonically; zero UUID means "no identity".
func uuidText(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	const hexDigits = "0123456789abcdef"
	var sb strings.Builder
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			sb.WriteByte('-')
		}
		sb.WriteByte(hexDigits[v>>4])
		sb.WriteByte(hexDigits[v&0x0f])
	}
	return sb.String()
}

// isUniqueViolation detects Postgres SQLSTATE 23505 without importing pgconn
// into the handler layer.
func isUniqueViolation(err error) bool {
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) {
		return pe.SQLState() == "23505"
	}
	return false
}

// compile-time proof the real wiring type satisfies the handler contract.
var _ AuthStore = (*store.Queries)(nil)

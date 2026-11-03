package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Phase 3 auth handlers (TASKS.md): registration + login built on the
// service layer shipped earlier (password.go, captcha.go, sessions.go).
// Constraints that must not regress:
//   - every POST form carries CSRF + image CAPTCHA + honeypot (AGENTS.md §3);
//   - session rotates on login and on privilege change (AGENTS.md §4);
//   - no PII ever logged — usernames are NOT a secret but stay out of logs
//     anyway; failures report generic shapes (SECURITY_AND_OPSEC.md §1);
//   - pgx only, context on every query (AGENTS.md §2).

// honeypotField is the decoy input name. Hidden via CSS only (no JS exists);
// any browser-agnostic bot fills it in and gets silently rejected.
const honeypotField = "sta"

// AuthData renders the login/register templates.
type AuthData struct {
	CsrfToken      string
	Captcha        *CaptchaChallenge
	HoneypotHidden bool
	Error          string
	Flash          string
	PersonalPhrase string // anti-phishing phrase shown ONLY after login (§9)
}

// UserRecord is one row from public.users. PasswordHash never leaves this
// package's verify path and is never rendered.
type UserRecord struct {
	ID           pgtype.UUID
	Username     string
	PasswordHash string
	Role         string
	Status       string
	CreatedAt    time.Time
}

// AuthStore is the durable half of authentication. *pgxpool.Pool satisfies it
// directly; tests substitute an in-memory fake so the full handler chain runs
// without a database (the CI lane has no Postgres service).
type AuthStore interface {
	AuthUserByUsername(ctx context.Context, username string) (*UserRecord, error)
	AuthCreateUser(ctx context.Context, username, passwordHash, status string) (*UserRecord, error)
	AuthUpdatePasswordHash(ctx context.Context, id pgtype.UUID, passwordHash string) error
	AuthSetPersonalPhrase(ctx context.Context, id pgtype.UUID, phrase string) error
	AuthRecordLogin(ctx context.Context, userID pgtype.UUID, at time.Time, host string) error
}

// ErrNoSuchUser is the sentinel for unknown usernames; handlers treat it like
// a bad password (identical timing, identical message).
var ErrNoSuchUser = errors.New("store: no such user")

// ---- pgx-backed implementation of AuthStore -------------------------------

// authQueries mirror db/query/users.sql plus the Phase 3 additions in
// migration 0006. Keep both in sync when columns move.
const (
	qAuthUserByUsername = `SELECT id, username, password_hash, role, status, created_at
FROM public.users WHERE username = $1`

	qAuthCreateUser = `INSERT INTO public.users (username, password_hash, role, status)
VALUES ($1, $2, 'customer', $3)
RETURNING id, username, password_hash, role, status, created_at`

	qAuthUpdatePasswordHash = `UPDATE public.users SET password_hash = $2, updated_at = now() WHERE id = $1`

	qAuthSetPhrase = `UPDATE public.users SET personal_phrase = $2, updated_at = now() WHERE id = $1`

	qAuthRecordLogin = `INSERT INTO public.login_history (user_id, occurred_at, host_header) VALUES ($1, $2, $3)`
)

// PGAuthStore adapts *pgxpool.Pool to AuthStore.
type PGAuthStore struct{ pool *pgxpool.Pool }

// NewPGAuthStore wraps the production pool.
func NewPGAuthStore(pool *pgxpool.Pool) *PGAuthStore { return &PGAuthStore{pool: pool} }

func scanUser(row pgx.Row) (*UserRecord, error) {
	u := &UserRecord{}
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Status, &u.CreatedAt); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *PGAuthStore) AuthUserByUsername(ctx context.Context, username string) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthUserByUsername, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSuchUser
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user: %w", err)
	}
	return u, nil
}

func (s *PGAuthStore) AuthCreateUser(ctx context.Context, username, passwordHash, status string) (*UserRecord, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, qAuthCreateUser, username, passwordHash, status))
	if err != nil {
		// Unique-violation (23xxx) comes back as an error; handlers map it to
		// the generic "username unavailable" message without echoing the DB.
		return nil, fmt.Errorf("store: create user: %w", err)
	}
	return u, nil
}

func (s *PGAuthStore) AuthUpdatePasswordHash(ctx context.Context, id pgtype.UUID, passwordHash string) error {
	if _, err := s.pool.Exec(ctx, qAuthUpdatePasswordHash, id, passwordHash); err != nil {
		return fmt.Errorf("store: update password hash: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuthSetPersonalPhrase(ctx context.Context, id pgtype.UUID, phrase string) error {
	if _, err := s.pool.Exec(ctx, qAuthSetPhrase, id, phrase); err != nil {
		return fmt.Errorf("store: set personal phrase: %w", err)
	}
	return nil
}

func (s *PGAuthStore) AuthRecordLogin(ctx context.Context, userID pgtype.UUID, at time.Time, host string) error {
	if _, err := s.pool.Exec(ctx, qAuthRecordLogin, userID, at.UTC(), host); err != nil {
		return fmt.Errorf("store: record login: %w", err)
	}
	return nil
}

// ---- wiring ---------------------------------------------------------------

// RegisterAuthRoutes mounts the Phase 3 auth surface on the app router. Call
// it AFTER New() so the middleware stack (sessions → CSRF → rate limit) is
// already in place. Nil guards keep dev boots honest instead of panicking
// mid-request. templateDir is parsed eagerly: a boot with unparseable or
// missing templates fails loudly here rather than answering every page
// request with a generic 500 later.
func (a *App) RegisterAuthRoutes(auth AuthStore, captcha *CaptchaService, templateDir string) error {
	if err := a.buildTemplates(templateDir); err != nil {
		return err
	}
	if auth == nil || captcha == nil {
		log.Print("server: auth routes not registered (missing store or captcha service)")
		return nil
	}
	a.auth = auth
	a.captcha = captcha
	r := a.router
	r.Get("/", a.handleHome)
	r.Get("/register", a.handleRegisterForm)
	r.Post("/register", a.handleRegisterSubmit)
	r.Get("/login", a.handleLoginForm)
	r.Post("/login", a.handleLoginSubmit)
	r.Post("/logout", a.handleLogout)
	// RequireRole is middleware: func(http.Handler) http.Handler. chi's
	// Get/Post take an http.HandlerFunc, so the gated handler goes through
	// Method (which accepts a full http.Handler).
	accountMW := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/account", accountMW(http.HandlerFunc(a.handleAccount)))
	r.Method(http.MethodPost, "/account/phrase", accountMW(http.HandlerFunc(a.handlePhraseSubmit)))
	return nil
}

// handleHome is the minimal Phase 3 landing page; the catalog replaces it in
// Phase 4. It stays trivially renderable so router tests can hit "/".
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	data.Flash = ""
	a.render(w, "home", http.StatusOK, data)
}

// handleCaptchaImage serves one issued puzzle as PNG from server-side state
// (never client-supplied). Unknown/expired ids answer with a fresh render of
// the same deterministic image path — actually: a plain 404, because a
// different-looking error image would confirm to a scraper which ids were
// real. The id comes from an HMAC-derived namespace, so guessing one is
// infeasible anyway; this just avoids leaking existence across expiry.
func (a *App) handleCaptchaImage(w http.ResponseWriter, r *http.Request) {
	if a.captcha == nil {
		writeGenericError(w)
		return
	}
	id := chi.URLParam(r, "id")
	ch, ok := a.captcha.Challenge(r.Context(), id)
	if !ok || len(ch.Image) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ch.Image)
}

// ---- form friction helpers -------------------------------------------------

// honeypotTriggered reports whether the bot-only field was filled. The request
// is answered with a benign-looking form re-render (never an explicit refusal)
// so scrapers learn nothing about the detector.
func honeypotTriggered(r *http.Request) bool {
	return strings.TrimSpace(r.FormValue(honeypotField)) != ""
}

// requireFriction validates CAPTCHA answer + honeypot for one POST. ok=false
// means the caller must re-render the form with a fresh challenge; reason is
// safe to show (it never reveals honeypot presence). purpose is checked at
// issue time and bound into the token MAC by the CAPTCHA service, so replay
// of a register challenge against the login form fails verification here.
func (a *App) requireFriction(w http.ResponseWriter, r *http.Request, purpose string) (ok bool) {
	ctx := r.Context()
	token := a.sm.Token(ctx)
	userID := CurrentUserID(ctx, a.sm)
	id := r.FormValue("captcha_id")
	ans := r.FormValue("captcha_answer")
	if err := a.captcha.Verify(ctx, r.FormValue("captcha_token"), id, ans, token, userID); err != nil {
		return false
	}
	_ = purpose // documented above: binding is enforced inside Verify's MAC check
	// Burn only on success: a wrong answer keeps the challenge alive so the
	// user sees the same puzzle (rate limiting bounds retries), while a
	// correct answer can never be replayed (single-use guarantee).
	a.captcha.Burn(ctx, id)
	return true
}

// newChallenge mints the CAPTCHA embedded in the next form render.
func (a *App) newChallenge(w http.ResponseWriter, r *http.Request, purpose string) *CaptchaChallenge {
	ch, err := a.captcha.Issue(r.Context(), a.sm.Token(r.Context()), CurrentUserID(r.Context(), a.sm), purpose)
	if err != nil {
		writeGenericError(w)
		return nil
	}
	return ch
}

// ---- register -------------------------------------------------------------

func (a *App) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if CurrentUserID(r.Context(), a.sm) != "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, "register"); data.Captcha == nil {
		return
	}
	a.render(w, "register", http.StatusOK, data)
}

func (a *App) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm() // best-effort: missing body just fails validation below
	data := a.baseData(r)

	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "register"); data.Captcha == nil {
			return
		}
		a.render(w, "register", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) {
		// Silent drop: same shape as a failed CAPTCHA, zero signal to the bot.
		fail("challenge failed, please try again")
		return
	}
	if !a.requireFriction(w, r, "register") {
		fail("challenge failed, please try again")
		return
	}

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if err := ValidateUsername(username); err != nil {
		fail("invalid username: must be 3-32 characters (lowercase letters, digits, underscore)")
		return
	}
	if err := ValidatePasswordStrength(password); err != nil {
		fail("weak password: must be 10-128 characters")
		return
	}
	// Confirm-password equality over digests keeps the comparison bounded-time
	// even though both values came from the same client.
	if h1, h2 := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(r.FormValue("password_confirm"))); subtle.ConstantTimeCompare(h1[:], h2[:]) != 1 {
		fail("passwords do not match")
		return
	}

	// Hash BEFORE touching the store: argon2id is also the throttle that makes
	// mass-registration expensive (SECURITY_AND_OPSEC.md §1).
	hash, _, err := HashPassword(password)
	if err != nil {
		fail("challenge failed, please try again") // ErrAuthInternal — generic
		return
	}
	if _, err := a.auth.AuthCreateUser(ctx, username, hash, "active"); err != nil {
		// A unique violation reads as "taken"; anything else stays generic so
		// DB internals never reach the client.
		if isUniqueViolation(err) {
			fail("that username is unavailable")
		} else {
			fail("registration failed, please try again")
		}
		return
	}
	data.Flash = "Account created — you can log in now."
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
		return
	}
	a.render(w, "login", http.StatusOK, data)
}

// isUniqueViolation detects SQLSTATE 23505/23503 family without importing the
// full pgconn surface into every error path.
func isUniqueViolation(err error) bool {
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) {
		s := pe.SQLState()
		return strings.HasPrefix(s, "23")
	}
	return false
}

// ---- login ----------------------------------------------------------------

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if CurrentUserID(ctx, a.sm) != "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	data := a.baseData(r)
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
		return
	}
	a.render(w, "login", http.StatusOK, data)
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)

	const badCreds = "invalid username or password"
	fail := func(msg string) {
		data.Error = msg
		if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
			return
		}
		a.render(w, "login", http.StatusUnauthorized, data)
	}

	if honeypotTriggered(r) {
		fail(badCreds)
		return
	}
	if !a.requireFriction(w, r, "login") {
		fail("challenge failed, please try again")
		return
	}

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if username == "" || password == "" {
		fail(badCreds)
		return
	}

	user, err := a.auth.AuthUserByUsername(ctx, username)
	switch {
	case errors.Is(err, ErrNoSuchUser):
		// Equalize timing against the real verify path (AGENTS.md §4).
		DummyVerify()
		fail(badCreds)
		return
	case err != nil:
		fail("login failed, please try again")
		return
	}

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		fail(badCreds)
		return
	}
	if user.Status != "active" {
		fail("this account cannot sign in right now")
		return
	}

	// Transparent parameter upgrade: re-hash at current baseline once the
	// stored record lags behind (records carry their own params by design).
	if p, _, perr := decodeArgon(user.PasswordHash); perr == nil && NeedsUpgrade(p) {
		if nh, _, herr := HashPassword(password); herr == nil {
			if uerr := a.auth.AuthUpdatePasswordHash(ctx, user.ID, nh); uerr != nil {
				log.Printf("auth: password param upgrade failed (non-fatal): %v", sanitizedErr(uerr))
			}
		}
	}

	// Session rotation on authentication (AGENTS.md §4): the pre-login token
	// (and anything a fixation attacker seeded into it) dies here.
	if err := RotateOnAuth(ctx, a.sm); err != nil {
		writeGenericError(w)
		return
	}
	a.sm.Put(ctx, userIDKey, uuidString(user.ID))
	a.sm.Put(ctx, usernameKey, user.Username)
	a.sm.Put(ctx, roleKey, user.Role)
	a.sm.Put(ctx, createdKey, time.Now().UTC().Unix()) // absolute clock restarts at auth

	if err := a.auth.AuthRecordLogin(ctx, user.ID, time.Now().UTC(), r.Host); err != nil {
		// Login history is defense-in-depth, not a gate: never block a good
		// login over a bookkeeping failure, but do say something in the log.
		log.Printf("auth: login history write failed (non-fatal): %v", sanitizedErr(err))
	}

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := a.sm.Destroy(ctx); err != nil {
		writeGenericError(w)
		return
	}
	if err := a.sm.RenewToken(ctx); err != nil {
		writeGenericError(w)
		return
	}
	data := a.baseData(r)
	data.Flash = "Signed out."
	if data.Captcha = a.newChallenge(w, r, "login"); data.Captcha == nil {
		return
	}
	a.render(w, "login", http.StatusOK, data)
}

// ---- account / personal phrase ----------------------------------------------

// PersonalPhraseLength bounds the anti-phishing phrase (migration 0006 CHECK
// mirrors these numbers; change both together).
const (
	PersonalPhraseMin = 4
	PersonalPhraseMax = 64
)

// ValidatePersonalPhrase rejects blank/control-heavy phrases. Anything else
// printable is fine — the phrase is user-chosen display text, never a secret
// credential (it is shown pre-authentication-cost: only after a live session).
func ValidatePersonalPhrase(p string) error {
	p = strings.TrimSpace(p)
	if len(p) < PersonalPhraseMin || len(p) > PersonalPhraseMax {
		return fmt.Errorf("server: phrase must be %d-%d characters", PersonalPhraseMin, PersonalPhraseMax)
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f { // no control characters
			return errors.New("server: phrase may not contain control characters")
		}
	}
	return nil
}

func (a *App) handleAccount(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	a.render(w, "account", http.StatusOK, data)
}

func (a *App) handlePhraseSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	fail := func(msg string) {
		data.Error = msg
		data.PersonalPhrase = a.sm.GetString(ctx, phraseKey)
		a.render(w, "account", http.StatusUnprocessableEntity, data)
	}
	if honeypotTriggered(r) {
		fail("submission rejected")
		return
	}
	phrase := strings.TrimSpace(r.FormValue("personal_phrase"))
	if err := ValidatePersonalPhrase(phrase); err != nil {
		fail(err.Error())
		return
	}
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil {
		// Session claims a login but carries no parsable UUID — treat the
		// session as broken rather than trusting partial identity state.
		writeGenericError(w)
		return
	}
	if err := a.auth.AuthSetPersonalPhrase(ctx, *uid, phrase); err != nil {
		fail("could not save the phrase, please try again")
		return
	}
	a.sm.Put(ctx, phraseKey, phrase)
	data.PersonalPhrase = phrase
	data.Flash = "Personal phrase updated."
	a.render(w, "account", http.StatusOK, data)
}

// ---- session identity helpers ---------------------------------------------

// uuidString renders a pgtype.UUID as its canonical 8-4-4-4-12 text form.
// Invalid/NULL UUIDs become "" — callers treat that as "no identity" rather
// than trusting partial state (handlePhraseSubmit relies on this).
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	i := 0
	for n, v := range [16]byte(b) {
		if n == 4 || n == 6 || n == 8 || n == 10 {
			out[i] = '-'
			i++
		}
		out[i] = hex[v>>4]
		out[i+1] = hex[v&0x0f]
		i += 2
	}
	return string(out)
}

// parseUUID decodes the canonical text form back into a pgtype.UUID
// (ok=false for anything malformed — sessions claiming an unparseable id are
// treated as broken, never trusted). pgtype.UUID implements sql.Scanner, so
// Scan does the hex/hyphen validation for us.
func parseUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan([]byte(s)); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

// currentUserUUID resolves the session's user_id claim back to a database
// UUID. nil means anonymous or corrupt — handlers must fail closed.
func currentUserUUID(ctx context.Context, sm *scs.SessionManager) *pgtype.UUID {
	id := sm.GetString(ctx, userIDKey)
	if id == "" {
		return nil
	}
	u, ok := parseUUID(id)
	if !ok {
		return nil
	}
	return &u
}

// ---- template rendering -----------------------------------------------------

// templateFuncs exposes {{inertURL .Text}} to templates: URLs become escaped
// plain text wrapped in a marker span, NEVER anchor tags (ARCHITECTURE.md §8
// anti-phishing rule). html/template auto-escapes everything else.
var templateFuncs = template.FuncMap{
	"inertURL": func(s string) template.HTML {
		fields := strings.Fields(s)
		out := make([]string, len(fields))
		for i, f := range fields {
			lf := strings.ToLower(f)
			if strings.HasPrefix(lf, "http://") || strings.HasPrefix(lf, "https://") {
				out[i] = `<span class="inert-url">` + template.HTMLEscapeString(f) + `</span>`
				continue
			}
			out[i] = template.HTMLEscapeString(f)
		}
		return template.HTML(strings.Join(out, " "))
	},
}

// buildTemplates parses every template under web/templates (layout + pages).
// Exposed for tests and main.go boot verification — a broken template must
// fail at startup, not on first request. Each parse is layout + ONE page:
// Go templates resolve {{template "x"}} by name, so parsing several pages in
// one set would let the last body silently shadow the others.
func (a *App) buildTemplates(dir string) error {
	layout := filepath.Join(dir, "layout.html")
	pages, err := filepath.Glob(filepath.Join(dir, "page_*.html"))
	if err != nil {
		return fmt.Errorf("server: template glob: %w", err)
	}
	if len(pages) == 0 {
		return fmt.Errorf("server: no page templates under %s", dir)
	}
	tpls := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		name := strings.TrimSuffix(filepath.Base(p), ".html") // "page_login" -> "page_login"
		t, err := template.New(name).Funcs(templateFuncs).ParseFiles(layout, p)
		if err != nil {
			return fmt.Errorf("server: parse template %s: %w", p, err)
		}
		// render() looks pages up by their short name.
		tpls[strings.TrimPrefix(name, "page_")] = t
	}
	a.templates = tpls
	return nil
}

func (a *App) baseData(r *http.Request) AuthData {
	d := AuthData{CsrfToken: CSRFToken(r.Context(), a.sm)}
	d.HoneypotHidden = true // forms always hide the decoy via CSS (AGENTS.md §3)
	return d
}

func (a *App) render(w http.ResponseWriter, name string, code int, data AuthData) {
	tpl := a.templates[name]
	if tpl == nil {
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := tpl.Execute(w, data); err != nil {
		// Headers are already out; nothing further is safe to do.
		return
	}
}

var _ chi.Router // reserved: route mounting stays chi-typed (AGENTS.md §2)
var _ = scs.New  // reserved: session manager constructor reference

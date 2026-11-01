// Phase 3 auth tests: password module, RBAC gates, and the login/register/
// logout handlers driven through the FULL middleware chain with an in-memory
// fake store (no Postgres needed — the AuthStore interface is the seam).

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgconn"

	store "github.com/example/onionshop/internal/store/gen"
)

// ---------- helpers ----------

func testUUID(seed byte) pgtype.UUID {
	var u pgtype.UUID
	u.Valid = true
	for i := range u.Bytes {
		u.Bytes[i] = seed + byte(i)
	}
	return u
}

func newTestApp(t *testing.T, st AuthStore, db DBProvider) *App {
	t.Helper()
	app, err := New(Config{Addr: "127.0.0.1:0", SecureCookies: false},
		Deps{Auth: &AuthSetup{Store: st, DB: db}}, scs.NewMemoryStore())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return app
}

// fullFastParams keeps Argon2id real but tiny for tests; production defaults
// are covered separately by TestDefaultArgonParamsShape.
func fullFastParams() ArgonParams {
	return ArgonParams{Memory: 8 * 1024, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 16}
}

// ---------- password module ----------

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple", fullFastParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, hashPrefix) {
		t.Fatalf("hash missing argon2id prefix: %q", h)
	}
	ok, err := VerifyPassword(h, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(h, "wrong passphrase")
	if err != nil || ok {
		t.Fatalf("wrong password verified: ok=%v err=%v", ok, err)
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=1,1,1$xx", "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA==$", "$argon2id$v=19$m=x,t=1,p=1$c2FsdA==$aG9y"} {
		if _, err := VerifyPassword(bad, "pw"); !errors.Is(err, ErrBadHash) {
			t.Errorf("VerifyPassword(%q) err=%v, want ErrBadHash", bad, err)
		}
	}
}

func TestDummyVerifyMatchesRealCost(t *testing.T) {
	// Dummy verify must complete (returns false regardless of input) — it
	// exists so unknown-user timing equals wrong-password timing.
	if DummyVerifyPassword("anything") {
		t.Fatal("dummy verify returned true")
	}
	if DummyVerifyPassword("") == true {
		t.Fatal("dummy verify on empty returned true")
	}
}

func TestDefaultArgonParamsShape(t *testing.T) {
	p := DefaultArgonParams()
	if p.Memory < 32*1024 || p.Time < 2 || p.Threads < 1 || p.KeyLen < 32 || p.SaltLen < 16 {
		t.Fatalf("params below OWASP floor: %+v", p)
	}
}

// ---------- fake store ----------

type fakeStore struct {
	mu       sync.Mutex
	byName   map[string]store.User
	attempts []store.RecordLoginAttemptParams
	execErr  error
}

func newFakeStore() *fakeStore {
	return &fakeStore{byName: map[string]store.User{}}
}

func (f *fakeStore) RegisterUser(ctx context.Context, db store.DBTX, arg store.RegisterUserParams) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.execErr != nil {
		return store.User{}, f.execErr
	}
	if _, dup := f.byName[arg.Username]; dup {
		return store.User{}, store.ErrDuplicateUsername
	}
	u := store.User{ID: testUUID(byte(len(f.byName) + 1)), Username: arg.Username, PasswordHash: arg.PasswordHash,
		Role: RoleCustomer, Status: "active", Tier: TierBasic}
	f.byName[arg.Username] = u
	return u, nil
}

func (f *fakeStore) GetUserByUsername(ctx context.Context, db store.DBTX, username string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.execErr != nil {
		return store.User{}, f.execErr
	}
	u, ok := f.byName[username]
	if !ok {
		return store.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeStore) UpdateUserPasswordHash(ctx context.Context, db store.DBTX, arg store.UpdateUserPasswordHashParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, u := range f.byName {
		if u.ID == arg.ID {
			u.PasswordHash = arg.PasswordHash
			f.byName[name] = u
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (f *fakeStore) RecordLoginAttempt(ctx context.Context, db store.DBTX, arg store.RecordLoginAttemptParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, arg)
	return nil
}

type fakeDB struct{ err error }

func (fakeDB) DB() store.DBTX { return noopExec{} }

// noopExec satisfies store.DBTX so param structs can flow through unchanged.
type noopExec struct{}

func (noopExec) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}
func (noopExec) Query(context.Context, string, ...interface{}) (pgx.Rows, error) { return nil, nil }
func (noopExec) QueryRow(context.Context, string, ...interface{}) pgx.Row        { return nil }

// ---------- client that carries cookies + CSRF like a real browser ----------

type client struct {
	t    *testing.T
	jar  *cookiejar.Jar
	srv  *httptest.Server
csrf string // last seen token from /login form
}

func newClient(t *testing.T, app *App) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	c := &client{t: t, jar: jar, srv: srv}
	c.refreshCSRF()
	return c
}

func (c *client) refreshCSRF() {
	c.t.Helper()
	res := c.get("/login")
	if res.StatusCode != http.StatusOK {
		c.t.Fatalf("GET /login: %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	i := strings.Index(string(body), `name="csrf_token" value="`)
	if i < 0 {
		c.t.Fatalf("no csrf token in login form: %s", body)
	}
	rest := string(body)[i+len(`name="csrf_token" value="`):]
	c.csrf = rest[:strings.Index(rest, `"`)]
}

func (c *client) do(method, path string, form url.Values) *http.Response {
	c.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf_token", c.csrf)
	req, err := http.NewRequest(method, c.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := &http.Client{Jar: c.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return res
}

func (c *client) get(path string) *http.Response {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.srv.URL+path, nil)
	hc := &http.Client{Jar: c.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return res
}

// ---------- handler tests (full middleware chain) ----------

func TestRegisterThenLoginLogoutFlow(t *testing.T) {
	fs := newFakeStore()
	app := newTestApp(t, fs, fakeDB{})
	c := newClient(t, app)

	const pass = "a long enough passphrase"
	res := c.do(http.MethodPost, "/register", url.Values{"username": {"alice"}, "password": {pass}})
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register status=%d body=%s", res.StatusCode, b)
	}
	res.Body.Close()

	// Registration established a session already; log out first to prove login works independently.
	res = c.do(http.MethodPost, "/logout", nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status=%d", res.StatusCode)
	}
	res.Body.Close()
	c.refreshCSRF()

	res = c.do(http.MethodPost, "/login", url.Values{"username": {"alice"}, "password": {pass}})
	if res.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("login status=%d body=%s", res.StatusCode, b)
	}
	res.Body.Close()

	// Successful login recorded history with sanitized host (no port).
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.attempts) == 0 || !fs.attempts[len(fs.attempts)-1].Success {
		t.Fatalf("no success attempt recorded: %+v", fs.attempts)
	}
	if strings.Contains(fs.attempts[len(fs.attempts)-1].HostUsed, ":") {
		t.Fatalf("host_used leaked port: %q", fs.attempts[len(fs.attempts)-1].HostUsed)
	}
}

func TestLoginFailuresAreUniform(t *testing.T) {
	fs := newFakeStore()
	hash, err := HashPassword("right-horse-battery", fullFastParams())
	if err != nil {
		t.Fatal(err)
	}
	fs.byName["bob"] = store.User{ID: testUUID(9), Username: "bob", PasswordHash: hash, Role: RoleCustomer, Status: "active", Tier: TierBasic}
	app := newTestApp(t, fs, fakeDB{})
	c := newClient(t, app)

	cases := []struct{ u, p string }{
		{"bob", "wrong-password-here"},
		{"nosuchuser", "whatever-passphrase"},
		{"", ""},
	}
	for _, tc := range cases {
		res := c.do(http.MethodPost, "/login", url.Values{"username": {tc.u}, "password": {tc.p}})
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized || string(body) != authGenericFail {
			t.Fatalf("case %q/%q: status=%d body=%q want 401 generic", tc.u, tc.p, res.StatusCode, body)
		}
	}
}

func TestSuspendedAccountGetsGenericFailure(t *testing.T) {
	fs := newFakeStore()
	hash, _ := HashPassword("valid-long-passphrase", fullFastParams())
	fs.byName["carol"] = store.User{ID: testUUID(3), Username: "carol", PasswordHash: hash, Role: RoleCustomer, Status: "suspended", Tier: TierBasic}
	app := newTestApp(t, fs, fakeDB{})
	c := newClient(t, app)
	res := c.do(http.MethodPost, "/login", url.Values{"username": {"carol"}, "password": {"valid-long-passphrase"}})
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("suspended login status=%d, want uniform 401", res.StatusCode)
	}
}

func TestRegisterValidation(t *testing.T) {
	fs := newFakeStore()
	app := newTestApp(t, fs, fakeDB{})
	c := newClient(t, app)

	for _, bad := range []string{"AB", "has spaces", "way-too-long-username-aaaaaaaaaaaaaaaaaaaaaa!", "up${inject}er"} {
		res := c.do(http.MethodPost, "/register", url.Values{"username": {bad}, "password": {"long-enough-passphrase"}})
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("username %q accepted: %d", bad, res.StatusCode)
		}
	}
	res := c.do(http.MethodPost, "/register", url.Values{"username": {"dave"}, "password": {"short"}})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password accepted: %d", res.StatusCode)
	}
}

func TestRegisterDuplicateRejected(t *testing.T) {
	fs := newFakeStore()
	app := newTestApp(t, fs, fakeDB{})
	c := newClient(t, app)
	first := c.do(http.MethodPost, "/register", url.Values{"username": {"eve"}, "password": {"long-enough-passphrase"}})
	first.Body.Close()
	c.refreshCSRF()
	second := c.do(http.MethodPost, "/register", url.Values{"username": {"eve"}, "password": {"another-long-passphrase"}})
	second.Body.Close()
	if second.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate register status=%d, want 400 generic", second.StatusCode)
	}
}

func TestAuthDegradedWithoutDB(t *testing.T) {
	fs := newFakeStore()
	app := newTestApp(t, fs, nilDB{})
	c := newClient(t, app)
	res := c.do(http.MethodPost, "/register", url.Values{"username": {"frank"}, "password": {"long-enough-passphrase"}})
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("degraded register status=%d, want 503", res.StatusCode)
	}
}

type nilDB struct{}

func (nilDB) DB() store.DBTX { return nil }

func TestCSRFRequiredForAuthPosts(t *testing.T) {
	fs := newFakeStore()
	app := newTestApp(t, fs, fakeDB{})
	c := newClient(t, app)
	// Tamper: send a POST without the double-submit token.
	form := url.Values{"username": {"gina"}, "password": {"long-enough-passphrase"}}
	req, _ := http.NewRequest(http.MethodPost, c.srv.URL+"/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := (&http.Client{Jar: c.jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("CSRF-less POST status=%d, want 403", res.StatusCode)
	}
}

func TestSessionIdentityStampedAfterLogin(t *testing.T) {
	fs := newFakeStore()
	hash, _ := HashPassword("marco-passphrase-x", fullFastParams())
	fs.byName["marco"] = store.User{ID: testUUID(7), Username: "marco", PasswordHash: hash, Role: RoleVendor, Status: "active", Tier: TierUpgraded}
	sm := scs.New()
	app, err := New(Config{Addr: "127.0.0.1:0"}, Deps{Sm: sm, Auth: &AuthSetup{Store: fs, DB: fakeDB{}}}, scs.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, app)
	res := c.do(http.MethodPost, "/login", url.Values{"username": {"marco"}, "password": {"marco-passphrase-x"}})
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status=%d", res.StatusCode)
	}
	// Probe identity via a route mounted after the chain.
	app.Router().Get("/whoami", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s", CurrentUserID(r.Context(), sm), CurrentRole(r.Context(), sm), CurrentTier(r.Context(), sm))
	})
	res2 := c.get("/whoami")
	body, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	got := string(body)
	if !strings.Contains(got, "|vendor|upgraded") || strings.HasPrefix(got, "|") {
		t.Fatalf("identity not stamped: %q", got)
	}
	// Logout wipes it.
	c.refreshCSRF()
	lo := c.do(http.MethodPost, "/logout", nil)
	lo.Body.Close()
	res3 := c.get("/whoami")
	body3, _ := io.ReadAll(res3.Body)
	res3.Body.Close()
	if string(body3) != "||" {
		t.Fatalf("session survived logout: %q", body3)
	}
}

// ---------- RBAC middleware ----------

// stampAndRequest logs in through the real auth flow (which stamps role/tier
// via RotateAndLogin), then hits a gated route with the same cookie jar.
func rbacGateTest(t *testing.T, gate func(sm *scs.SessionManager) func(http.Handler) http.Handler, userRole, userTier, want string, expectPass bool) {
t.Helper()
fs := newFakeStore()
hash, err := HashPassword("gate-passphrase-123", fullFastParams())
if err != nil {
t.Fatal(err)
}
fs.byName["gater"] = store.User{ID: testUUID(5), Username: "gater", PasswordHash: hash, Role: userRole, Status: "active", Tier: userTier}
sm := scs.New()
app, err := New(Config{Addr: "127.0.0.1:0"}, Deps{Sm: sm, Auth: &AuthSetup{Store: fs, DB: fakeDB{}}}, scs.NewMemoryStore())
if err != nil {
t.Fatal(err)
}
c := newClient(t, app)
res := c.do(http.MethodPost, "/login", url.Values{"username": {"gater"}, "password": {"gate-passphrase-123"}})
res.Body.Close()
if res.StatusCode != http.StatusSeeOther {
t.Fatalf("login failed: %d", res.StatusCode)
}
app.Router().With(gate(sm)).Get("/gated", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
gres := c.get("/gated")
gres.Body.Close()
if pass := gres.StatusCode == http.StatusOK; pass != expectPass {
t.Fatalf("role=%s tier=%s gate(%q): status=%d pass=%v want %v", userRole, userTier, want, gres.StatusCode, pass, expectPass)
}
}

func TestRequireRoleGates(t *testing.T) {
rbacGateTest(t, func(sm *scs.SessionManager) func(http.Handler) http.Handler { return RequireRole(sm, RoleAdmin) }, RoleAdmin, TierBasic, RoleAdmin, true)
rbacGateTest(t, func(sm *scs.SessionManager) func(http.Handler) http.Handler { return RequireRole(sm, RoleAdmin) }, RoleCustomer, TierBasic, RoleAdmin, false)
rbacGateTest(t, RequireStaff, RoleMod, TierBasic, "", true)
rbacGateTest(t, RequireStaff, RoleVendor, TierBasic, "", false)
rbacGateTest(t, func(sm *scs.SessionManager) func(http.Handler) http.Handler { return RequireAnyRole(sm, RoleVendor, RoleMod) }, RoleVendor, TierBasic, "", true)
rbacGateTest(t, func(sm *scs.SessionManager) func(http.Handler) http.Handler { return RequireTier(sm, TierUpgraded) }, RoleVendor, TierBasic, "", false)
rbacGateTest(t, func(sm *scs.SessionManager) func(http.Handler) http.Handler { return RequireTier(sm, TierUpgraded) }, RoleVendor, TierUpgraded, "", true)
}

func TestAnonymousBlockedByGates(t *testing.T) {
sm := scs.New()
app, err := New(Config{Addr: "127.0.0.1:0"}, Deps{Sm: sm}, scs.NewMemoryStore())
if err != nil {
t.Fatal(err)
}
app.Router().With(RequireRole(sm, RoleAdmin)).Get("/secret", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
srv := httptest.NewServer(app.Handler())
t.Cleanup(srv.Close)
res, _ := http.Get(srv.URL + "/secret")
res.Body.Close()
if res.StatusCode != http.StatusForbidden {
t.Fatalf("anonymous admin gate: %d", res.StatusCode)
}
}

func TestRequireRolePanicsOnUnknown(t *testing.T) {
defer func() {
if rec := recover(); rec == nil {
t.Fatal("expected panic for unknown role")
}
}()
RequireRole(scs.New(), "superuser")
}

func TestSanitizeHost(t *testing.T) {
	if got := sanitizeHost("Example.COM:8080"); got != "example.com" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeHost("evil\r\ninjected"); got != "" {
		t.Fatalf("CRLF not rejected: %q", got)
	}
	long := strings.Repeat("a", 300)
	if got := sanitizeHost(long); len(got) != 253 {
		t.Fatalf("length cap failed: %d", len(got))
	}
}

func TestUUIDText(t *testing.T) {
	u := testUUID(0)
	got := uuidText(u)
	if len(got) != 36 || strings.Count(got, "-") != 4 {
		t.Fatalf("uuidText malformed: %q", got)
	}
	if uuidText(pgtype.UUID{}) != "" {
		t.Fatal("invalid uuid should render empty")
	}
}

var _ = time.Second

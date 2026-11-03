package server

import (
	"context"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Phase 3 auth handler tests: fake AuthStore covering the 4-arg
// AuthRecordLogin contract (UTC time + r.Host), login-history failure
// isolation, template-set construction, and RegisterAuthRoutes boot policy.
// No Postgres exists in CI (AGENTS.md: pure-logic test lanes), so every store
// interaction here rides the in-memory fake below.

// ---- fake AuthStore --------------------------------------------------------

type recordedLogin struct {
	userID pgtype.UUID
	at     time.Time
	host   string
}

// fakeAuthStore implements AuthStore entirely in memory. recordLoginErr makes
// the login-history write fail on demand (the handler must stay non-blocking);
// everything else succeeds so happy paths exercise real code.
type fakeAuthStore struct {
	mu sync.Mutex

	users          map[string]*UserRecord
	nextID         int
	recordLoginN   int
	recorded       []recordedLogin
	recordLoginErr error
	setPhraseErr   error
}

func newFakeAuthStore(t *testing.T) *fakeAuthStore {
	t.Helper()
	return &fakeAuthStore{users: map[string]*UserRecord{}, nextID: 1}
}

// seedUser installs an already-hashed user under name (lower-cased like the
// handlers do) and returns the plaintext password it verifies against.
func (f *fakeAuthStore) seedUser(t *testing.T, name, role string) (password string, id pgtype.UUID) {
	t.Helper()
	hash, _, err := HashPassword("correct horse battery staple 9")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	u := &UserRecord{
		ID:           idFromCounter(f.nextID),
		Username:     strings.ToLower(name),
		PasswordHash: hash,
		Role:         role,
		Status:       "active",
		CreatedAt:    time.Now().UTC(),
	}
	f.nextID++
	f.users[u.Username] = u
	return "correct horse battery staple 9", u.ID
}

// idFromCounter builds a deterministic valid pgtype.UUID from a small counter
// (bytes are arbitrary but well-formed; only uniqueness matters to handlers).
func idFromCounter(n int) pgtype.UUID {
	var b [16]byte
	b[0] = byte(n)
	b[15] = byte(n>>8) | 0x01
	return pgtype.UUID{Bytes: b, Valid: true}
}

func (f *fakeAuthStore) AuthUserByUsername(_ context.Context, username string) (*UserRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[strings.ToLower(username)]
	if !ok {
		return nil, ErrNoSuchUser
	}
	cp := *u
	return &cp, nil
}

func (f *fakeAuthStore) AuthCreateUser(_ context.Context, username, passwordHash, status string) (*UserRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.users[username]; dup {
		return nil, errors.New(`store: create user: ERROR: duplicate key value violates unique constraint (SQLSTATE 23505)`)
	}
	u := &UserRecord{
		ID:           idFromCounter(f.nextID),
		Username:     username,
		PasswordHash: passwordHash,
		Role:         "customer",
		Status:       status,
		CreatedAt:    time.Now().UTC(),
	}
	f.nextID++
	f.users[username] = u
	cp := *u
	return &cp, nil
}

func (f *fakeAuthStore) AuthUpdatePasswordHash(_ context.Context, id pgtype.UUID, passwordHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.ID == id {
			u.PasswordHash = passwordHash
			return nil
		}
	}
	return errors.New("store: update password hash: no such user")
}

func (f *fakeAuthStore) AuthSetPersonalPhrase(_ context.Context, id pgtype.UUID, phrase string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setPhraseErr != nil {
		return f.setPhraseErr
	}
	for _, u := range f.users {
		if u.ID == id {
			return nil
		}
	}
	return errors.New("store: set personal phrase: no such user")
}

// AuthRecordLogin matches the production 4-arg interface exactly
// (ctx, userID, at, host) — any signature drift breaks compilation here.
func (f *fakeAuthStore) AuthRecordLogin(_ context.Context, userID pgtype.UUID, at time.Time, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLoginN++
	if f.recordLoginErr != nil {
		return f.recordLoginErr
	}
	f.recorded = append(f.recorded, recordedLogin{userID: userID, at: at, host: host})
	return nil
}

func (f *fakeAuthStore) history() ([]recordedLogin, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]recordedLogin(nil), f.recorded...)
	return out, f.recordLoginN
}

// ---- full-chain login helper ------------------------------------------------

// newAuthAppForTest wires the complete middleware chain (rate limiting OFF
// via the sanctioned test helper; Redis nil would otherwise fail-close every
// POST) plus RegisterAuthRoutes over web/templates relative to the repo root.
func newAuthAppForTest(t *testing.T, store AuthStore) *App {
	t.Helper()
	app := routerWithoutRateLimitForTest(t, Config{SecureCookies: false}, Deps{})
	capSvc, err := NewCaptchaService(nil, []byte("test-secret-test-secret"))
	if err != nil {
		t.Fatalf("NewCaptchaService: %v", err)
	}
	dir, err := filepath.Abs(filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterAuthRoutes(store, capSvc, dir); err != nil {
		t.Fatalf("RegisterAuthRoutes: %v", err)
	}
	return app
}

// postForm runs one form POST through the whole router with the session
// cookie captured from getTarget, solving the CAPTCHA embedded in that GET.
// Returns the response recorder.
func postForm(t *testing.T, app *App, getTarget string, c *httpClient, path string, extra url.Values) (rec *httptest.ResponseRecorder, formBody string) {
	t.Helper()
	recGet, body := c.get(t, getTarget)
	if recGet.Code != 200 {
		t.Fatalf("GET %s = %d, want 200 (body %q)", getTarget, recGet.Code, truncate(body))
	}
	csrf := extractInput(t, body, "csrf_token")
	capID := extractInput(t, body, "captcha_id")
	capTok := extractInput(t, body, "captcha_token")
	prompt := extractText(t, body, "CAPTCHA:") // e.g. "7 + 4 ="
	sum := parsePromptSum(t, prompt)

	form := url.Values{}
	form.Set("csrf_token", csrf)
	form.Set("captcha_id", capID)
	form.Set("captcha_token", capTok)
	form.Set("captcha_answer", sum)
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	return c.postForm(t, path, form), body
}

// httpClient carries the live session cookie across requests (mirrors a
// browser; scs writes it on every LoadAndSave response).
type httpClient struct {
	app     *App
	cookies map[string]string
}

func newHTTPClient(app *App) *httpClient {
	return &httpClient{app: app, cookies: map[string]string{}}
}

func (c *httpClient) do(t *testing.T, method, target string, body io.Reader, contentType string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for n, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: n, Value: v})
	}
	rec := httptest.NewRecorder()
	c.app.Handler().ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		c.cookies[ck.Name] = ck.Value
	}
	return rec, rec.Body.String()
}

func (c *httpClient) get(t *testing.T, target string) (*httptest.ResponseRecorder, string) {
	return c.do(t, "GET", target, nil, "")
}

func (c *httpClient) postForm(t *testing.T, target string, form url.Values) *httptest.ResponseRecorder {
	rec, _ := c.do(t, "POST", target, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	return rec
}

// ---- login history: success records UTC time + r.Host -----------------------

func TestLoginRecordsHistoryWithUTCTimeAndHost(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	password, uid := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	before := time.Now().UTC()
	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	after := time.Now().UTC()

	if rec.Code != 303 {
		t.Fatalf("POST /login = %d, want 303 (body %q)", rec.Code, truncate(rec.Body.String()))
	}
	if got := rec.Header().Get("Location"); got != "/account" {
		t.Errorf("redirect Location = %q, want /account", got)
	}

	hist, n := store.history()
	if n != 1 || len(hist) != 1 {
		t.Fatalf("AuthRecordLogin calls = %d (recorded %d), want exactly 1", n, len(hist))
	}
	got := hist[0]
	if got.userID != uid {
		t.Errorf("history user ID = %v, want %v", uuidString(got.userID), uuidString(uid))
	}
	// The schema stores TIMESTAMP WITHOUT TIME ZONE (migration 0006): the
	// handler MUST hand over a UTC wall clock — r.Host likewise comes straight
	// from the request, never from forwarding headers.
	if got.at.Location() != time.UTC {
		t.Errorf("history occurred_at location = %v, want UTC", got.at.Location())
	}
	if got.at.Before(before.Add(-time.Second)) || got.at.After(after.Add(time.Second)) {
		t.Errorf("history occurred_at %v outside request window [%v, %v]", got.at, before, after)
	}
	// httptest sets Host to "example.com"; that is what r.Host must record.
	if got.host != "example.com" {
		t.Errorf("history host_header = %q, want %q", got.host, "example.com")
	}
}

// ---- login history failure must NOT block the login -------------------------

func TestLoginHistoryWriteErrorDoesNotBlockLogin(t *testing.T) {
	var logBuf syncBuffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	store := newFakeAuthStore(t)
	store.recordLoginErr = errors.New("insert into public.login_history failed")
	password, _ := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	if rec.Code != 303 {
		t.Fatalf("history-write failure blocked login: got %d, want 303 (body %q)",
			rec.Code, truncate(rec.Body.String()))
	}
	if got := rec.Header().Get("Location"); got != "/account" {
		t.Errorf("Location = %q, want /account", got)
	}
	if _, n := store.history(); n != 1 {
		t.Errorf("AuthRecordLogin calls = %d, want 1 (attempted once)", n)
	}
	if msg := logBuf.String(); !strings.Contains(msg, "login history") {
		t.Errorf("expected non-fatal log line mentioning login history, got %q", msg)
	}
}

// ---- honeypot + CSRF ride the same chain (regression smoke) ------------------

func TestLoginHoneypotSilentlyRejected(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	password, _ := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
		"sta":      {"bot-filled"},
	})
	if rec.Code != 401 {
		t.Errorf("honeypot POST /login = %d, want 401 silent re-render", rec.Code)
	}
	if _, n := store.history(); n != 0 {
		t.Errorf("honeypot submission reached login history (%d writes)", n)
	}
}

// ---- buildTemplates ----------------------------------------------------------

func writeTemplateFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuildTemplatesFailsOnMissingDir(t *testing.T) {
	a := &App{}
	err := a.buildTemplates(filepath.Join(t.TempDir(), "definitely-not-here"))
	if err == nil {
		t.Fatal("buildTemplates on missing dir: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no page templates") {
		t.Errorf("error %q should report the empty page glob", err)
	}
}

func TestBuildTemplatesFailsOnUnparseablePage(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "layout.html", `{{define "layout"}}{{block "body" .}}x{{end}}{{end}}`)
	writeTemplateFile(t, dir, "page_broken.html", `{{template "layout" .}}{{define "body"}}{{.Unclosed`)
	a := &App{}
	if err := a.buildTemplates(dir); err == nil {
		t.Fatal("buildTemplates on unparseable page: want error, got nil")
	} else if !strings.Contains(err.Error(), "parse template") {
		t.Errorf("error %q should name the failing page", err)
	}
}

func TestBuildTemplatesFailsWhenNoPages(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "layout.html", `{{define "layout"}}{{block "body" .}}x{{end}}{{end}}`)
	writeTemplateFile(t, dir, "not_a_page.html", `hello`)
	a := &App{}
	if err := a.buildTemplates(dir); err == nil {
		t.Fatal("buildTemplates with zero page_*.html: want error, got nil")
	}
}

func TestBuildTemplatesSucceedsOnShippedWebTemplates(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{}
	if err := a.buildTemplates(dir); err != nil {
		t.Fatalf("buildTemplates(%s): %v", dir, err)
	}
	// render() keys are short names ("page_login" -> "login"). Execute the
	// layout by name — that is exactly what render() does at request time;
	// the associated-file name itself is only a parse namespace, not a body.
	for _, key := range []string{"home", "login", "register", "account"} {
		tpl := a.templates[key]
		if tpl == nil {
			t.Errorf("template %q not registered (keys: %v)", key, templateKeys(a.templates))
			continue
		}
		if err := tpl.ExecuteTemplate(io.Discard, "layout", AuthData{CsrfToken: "tok", Captcha: &CaptchaChallenge{ID: "i", Prompt: "1 + 1 =", Token: "t"}}); err != nil {
			t.Errorf("execute %q: %v", key, err)
		}
	}
}

func templateKeys(m map[string]*template.Template) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---- RegisterAuthRoutes boot policy ------------------------------------------

func TestRegisterAuthRoutesPropagatesTemplateError(t *testing.T) {
	store := newFakeAuthStore(t)
	capSvc, err := NewCaptchaService(nil, []byte("test-secret-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	app := routerWithoutRateLimitForTest(t, Config{SecureCookies: false}, Deps{})
	missing := filepath.Join(t.TempDir(), "nope")
	if err := app.RegisterAuthRoutes(store, capSvc, missing); err == nil {
		t.Fatal("RegisterAuthRoutes accepted a missing template dir; boot must fail loudly")
	}
}

func TestRegisterAuthRoutesNilStoreLogsAndSkips(t *testing.T) {
	var logBuf syncBuffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	capSvc, err := NewCaptchaService(nil, []byte("test-secret-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	app := routerWithoutRateLimitForTest(t, Config{SecureCookies: false}, Deps{})
	dir, err := filepath.Abs(filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	// Templates still parse first (a broken dir fails even without a store),
	// then the nil store degrades gracefully: nil error + loud log.
	if err := app.RegisterAuthRoutes(nil, capSvc, dir); err != nil {
		t.Fatalf("nil-store registration returned error %v; want log-and-skip", err)
	}
	if msg := logBuf.String(); !strings.Contains(msg, "auth routes not registered") {
		t.Errorf("missing degradation log line, got %q", msg)
	}
	// Skipped means skipped: "/" must still be the anonymous 404 redirect,
	// not handleHome (which would prove the route was mounted anyway).
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 303 || rec.Header().Get("Location") != "/" {
		t.Errorf("GET / after skip = %d Location=%q, want 303 self-redirect (route unmounted)",
			rec.Code, rec.Header().Get("Location"))
	}
}

// ---- helpers -------------------------------------------------------------------

// warnLogTo keeps expected non-fatal handler logs out of test noise while
// still failing the test if nothing ever reads them (keeps output tidy).
func warnLogTo(t *testing.T) {
	t.Helper()
	var buf syncBuffer
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		if t.Failed() && buf.Len() > 0 {
			t.Logf("captured log during failure:\n%s", buf.String())
		}
	})
}

// syncBuffer is a mutex-guarded bytes.Buffer (log package requires
// goroutine-safe writers when tests run parallel handlers).
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf.String())
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// httpRequestPromptSum parses "7 + 4 =" into "11" — the answer the shared
// captcha_test helper cannot be reused for directly here because it takes a
// full prompt string; keep one local parser so form extraction stays honest.
func parsePromptSum(t *testing.T, prompt string) string {
	t.Helper()
	// prompt is "<a> + <b> =" as rendered by html/template — "+" inside text
	// context arrives entity-escaped ("&#43;").
	prompt = strings.ReplaceAll(prompt, "&#43;", "+")
	repl := strings.NewReplacer("=", "", "+", " ")
	fields := strings.Fields(repl.Replace(prompt))
	if len(fields) != 2 {
		t.Fatalf("unexpected captcha prompt %q", prompt)
	}
	a, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("prompt %q: %v", prompt, err)
	}
	b, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("prompt %q: %v", prompt, err)
	}
	return strconv.Itoa(a + b)
}

// extractInput pulls the value attribute of <input ... name="want" ...>.
func extractInput(t *testing.T, body, want string) string {
	t.Helper()
	idx := strings.Index(body, `name="`+want+`"`)
	if idx < 0 {
		t.Fatalf("hidden input %q not found in body:\n%s", want, truncate(body))
	}
	rest := body[idx:]
	end := strings.Index(rest, `>`)
	if end < 0 {
		t.Fatalf("unterminated input tag for %q", want)
	}
	tag := rest[:end]
	vi := strings.Index(tag, `value="`)
	if vi < 0 {
		return ""
	}
	tag = tag[vi+len(`value="`):]
	ve := strings.Index(tag, `"`)
	if ve < 0 {
		t.Fatalf("unterminated value attribute for %q", want)
	}
	s := tag[:ve]
	// html/template escapes attribute values; undo the handful of entities
	// base64url tokens can carry.
	return strings.NewReplacer("&amp;", "&", "&#43;", "+", "&#61;", "=", "&#47;", "/").Replace(s)
}

// extractText grabs the visible text right after a marker, up to the next tag.
func extractText(t *testing.T, body, marker string) string {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("marker %q not found in body:\n%s", marker, truncate(body))
	}
	rest := body[i+len(marker):]
	if j := strings.IndexAny(rest, "<"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(strings.ReplaceAll(rest, "\n", " "))
}

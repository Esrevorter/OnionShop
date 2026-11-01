package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/redis/go-redis/v9"
)

// newTestSessions returns an scs manager over scs' in-memory store with dev
// cookie settings — the same composition main.go uses when no backend is
// configured.
func newTestSessions(t *testing.T) *scs.SessionManager {
	t.Helper()
	return NewSessions(scs.New().Store, SessionConfig{
		IdleTimeout:     5 * time.Minute,
		AbsoluteTimeout: time.Hour,
		SecureCookies:   false, // httptest serves plain HTTP
	})
}

// sessionCookie extracts one named cookie from a recorder's response.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder, wantPresent bool) *http.Cookie {
	t.Helper()
	cs := rec.Result().Cookies()
	for i := range cs {
		if cs[i].Name == SessionCookieName {
			return cs[i]
		}
	}
	if wantPresent {
		t.Fatal("session cookie missing from response")
	}
	return nil
}

// TestSecurityHeadersCSP guards the exact ARCHITECTURE.md §3 string and the
// header set applied to every response, including error pages.
func TestSecurityHeadersCSP(t *testing.T) {
	if CSP != "default-src 'self'; script-src 'none'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; "+
		"upgrade-insecure-requests" {
		t.Fatalf("CSP drifted from ARCHITECTURE.md §3; nginx re-emits this exact policy — change both together:\n%s", CSP)
	}
	rec := httptest.NewRecorder()
	SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for k, want := range map[string]string{
		"Content-Security-Policy": CSP,
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Pragma":                  "no-cache",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
}

// TestNoBrowserHSTS: emitting Strict-Transport-Security against a self-signed
// onion cert is a permanent lockout footgun — assert it never appears.
func TestNoBrowserHSTS(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must never be emitted browser-facing (SECURITY_AND_OPSEC.md §1)")
	}
}

func TestStripForwardedHeaders(t *testing.T) {
	var seen http.Header
	mw := StripForwardedHeaders()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	dropped := []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Forwarded-Proto"}
	for _, name := range dropped {
		req.Header.Set(name, "evil")
	}
	req.Header.Set("X-Legit", "keep")
	mw.ServeHTTP(httptest.NewRecorder(), req)
	for _, name := range dropped {
		if seen.Get(name) != "" {
			t.Errorf("%s survived the strip", name)
		}
	}
	if seen.Get("X-Legit") != "keep" {
		t.Error("unrelated header was dropped")
	}
}

// TestSessionCookieFlags asserts AGENTS.md §4 exactly: HttpOnly + Secure +
// SameSite=Strict, host-only.
func TestSessionCookieFlags(t *testing.T) {
	sm := newTestSessions(t)
	sm.Cookie.Secure = true // production posture
	rec := httptest.NewRecorder()
	sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), createdKey, time.Now().UTC().Unix())
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	c := sessionCookie(t, rec, true)
	if !c.HttpOnly || !c.Secure {
		t.Errorf("HttpOnly=%v Secure=%v, want both true", c.HttpOnly, c.Secure)
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.Domain != "" {
		t.Errorf("cookie must be host-only for an onion, got Domain=%q", c.Domain)
	}
}

// TestAbsoluteExpiryRenewsStaleSession drives AbsoluteExpiry underneath
// LoadAndSave (its contract — like CSRFLoad, it requires session data in the
// request context). A tiny timeout makes the creation stamp expire between
// requests; the visitor must silently continue with a renewed token.
func TestAbsoluteExpiryRenewsStaleSession(t *testing.T) {
	sm := newTestSessions(t)

	var sawCreated int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCreated = sm.GetInt64(r.Context(), createdKey)
		w.WriteHeader(http.StatusOK)
	})
	chain := func(timeout time.Duration) http.Handler {
		return sm.LoadAndSave(AbsoluteExpiry(sm, timeout)(inner))
	}

	// Request 1: fresh session gets stamped.
	rec1 := httptest.NewRecorder()
	chain(time.Hour).ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec1.Code != http.StatusOK || sawCreated == 0 {
		t.Fatalf("first pass: code=%d created=%d, want 200 + fresh stamp", rec1.Code, sawCreated)
	}
	token := sessionCookie(t, rec1, true).Value

	time.Sleep(5 * time.Millisecond)

	// Request 2 with a nanosecond window: expiry fires, token is renewed.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	rec2 := httptest.NewRecorder()
	chain(time.Nanosecond).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expired session should silently continue anonymously, got %d", rec2.Code)
	}
	newToken := sessionCookie(t, rec2, true).Value
	if newToken == token {
		t.Errorf("absolute expiry did not renew the token (%q)", token)
	}
	if sawCreated == 0 {
		t.Error("createdKey was not re-stamped after expiry")
	}
}

// TestCSRFDoubleSubmit walks the full mint → accept → reject path.
func TestCSRFDoubleSubmit(t *testing.T) {
	sm := newTestSessions(t)
	chain := CSRFLoad(sm)(CSRFProtect(sm)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	smChain := sm.LoadAndSave(chain)

	// GET mints the token inside the session; capture the session cookie.
	recGET := httptest.NewRecorder()
	smChain.ServeHTTP(recGET, httptest.NewRequest(http.MethodGet, "/", nil))
	if recGET.Code != http.StatusOK {
		t.Fatalf("GET through csrf chain = %d", recGET.Code)
	}
	cookie := sessionCookie(t, recGET, true)

	// Probe handler reads the minted token back out of the live session.
	var tok string
	probe := sm.LoadAndSave(CSRFLoad(sm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok = CSRFToken(r.Context(), sm)
		w.WriteHeader(http.StatusOK)
	})))
	recProbe := httptest.NewRecorder()
	reqProbe := httptest.NewRequest(http.MethodGet, "/", nil)
	reqProbe.AddCookie(cookie)
	probe.ServeHTTP(recProbe, reqProbe)
	if len(tok) < 16 {
		t.Fatalf("minted CSRF token too short: %q", tok)
	}

	// POST with the correct field passes.
	recOK := httptest.NewRecorder()
	reqOK := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("csrf_token="+tok))
	reqOK.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqOK.AddCookie(cookie)
	smChain.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("valid CSRF POST = %d, want 200", recOK.Code)
	}

	// POST with a wrong token fails closed with the uniform 403.
	recBad := httptest.NewRecorder()
	reqBad := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("csrf_token=nope"))
	reqBad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBad.AddCookie(cookie)
	smChain.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF POST = %d, want 403", recBad.Code)
	}

	// Header variant accepted (reserved for the SPKI-pinned CLI).
	recHdr := httptest.NewRecorder()
	reqHdr := httptest.NewRequest(http.MethodPost, "/x", nil)
	reqHdr.Header.Set("X-CSRF-Token", tok)
	reqHdr.AddCookie(cookie)
	smChain.ServeHTTP(recHdr, reqHdr)
	if recHdr.Code != http.StatusOK {
		t.Fatalf("header CSRF POST = %d, want 200", recHdr.Code)
	}
}

func TestIsSafeMethod(t *testing.T) {
	for m, want := range map[string]bool{
		http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true,
		http.MethodPost: false, http.MethodPut: false, http.MethodPatch: false,
		http.MethodDelete: false,
	} {
		if got := isSafeMethod(m); got != want {
			t.Errorf("isSafeMethod(%s) = %v, want %v", m, got, want)
		}
	}
}

// TestRateLimitFailurePolicy: nil RDB models Redis being down — writes must
// fail CLOSED (503), reads must fail OPEN (deliberate asymmetry).
func TestRateLimitFailurePolicy(t *testing.T) {
	mw := RateLimit(LimiterConfig{
		RDB:       nil, // redis down / unconfigured
		Buckets:   DefaultBuckets(),
		UserID:    func(*http.Request) string { return "" },
		SessionID: func(*http.Request) string { return "tok" },
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	recGet := httptest.NewRecorder()
	mw.ServeHTTP(recGet, httptest.NewRequest(http.MethodGet, "/", nil))
	if recGet.Code != http.StatusOK {
		t.Errorf("GET with redis down = %d, want fail-open 200", recGet.Code)
	}

	recPost := httptest.NewRecorder()
	mw.ServeHTTP(recPost, httptest.NewRequest(http.MethodPost, "/login", nil))
	if recPost.Code != http.StatusServiceUnavailable {
		t.Errorf("POST with redis down = %d, want fail-closed 503", recPost.Code)
	}
	if recPost.Header().Get(HeaderRetryAfter) == "" {
		t.Error("fail-closed response missing Retry-After")
	}
}

// TestBucketForRequest pins the bucket table, including the rule that liveness
// probes are never throttled (a false alarm is worse than the traffic shed).
func TestBucketForRequest(t *testing.T) {
	fn := bucketForRequest(nil)
	if b := fn(httptest.NewRequest(http.MethodGet, "/healthz", nil), ""); b != "" {
		t.Errorf("/healthz bucket = %q, want \"\" (exempt)", b)
	}
	cases := map[string]string{
		"/login":         "login",
		"/auth/logout":   "login",
		"/register":      "register",
		"/captcha/x.png": "captcha",
		"/messages/1":    "default", // anonymous message traffic rides default
		"/anything":      "default",
	}
	for path, want := range cases {
		if got := fn(httptest.NewRequest(http.MethodGet, path, nil), ""); got != want {
			t.Errorf("bucket(%s) = %q, want %q", path, got, want)
		}
	}
	if got := fn(httptest.NewRequest(http.MethodGet, "/messages/1", nil), "u42"); got != "message" {
		t.Errorf("authenticated /messages bucket = %q, want message", got)
	}
	// Custom override wins wholesale.
	custom := bucketForRequest(func(*http.Request, string) string { return "mine" })
	if got := custom(httptest.NewRequest(http.MethodGet, "/healthz", nil), ""); got != "mine" {
		t.Errorf("custom bucket = %q, want mine", got)
	}
}

// TestRateLimiterNilClient asserts Allow/Remaining report ErrRedisUnavailable
// rather than panicking when handed no client (degraded boot).
func TestRateLimiterNilClient(t *testing.T) {
	l := NewRateLimiter((*redis.Client)(nil), Bucket{Name: "x", Limit: 1, Window: time.Minute})
	if _, err := l.Allow(context.Background(), "id"); err != ErrRedisUnavailable {
		t.Errorf("Allow nil-client err = %v, want ErrRedisUnavailable", err)
	}
	if _, err := l.Remaining(context.Background(), "id"); err != ErrRedisUnavailable {
		t.Errorf("Remaining nil-client err = %v, want ErrRedisUnavailable", err)
	}
}

func TestHashKeyStableAndShort(t *testing.T) {
	a, b := hashKey("session-token-abc"), hashKey("session-token-abc")
	if a != b {
		t.Error("hashKey not deterministic")
	}
	if a == hashKey("session-token-abd") {
		t.Error("collision on trivially different inputs")
	}
	if strings.Contains(a, "session-token") {
		t.Error("raw identity leaked into key material")
	}
}

func TestNewRandomTokenLength(t *testing.T) {
	tok, err := NewRandomToken(0) // 0 => default 32 bytes
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 43 { // 32 bytes base64url = 43 chars
		t.Errorf("token too short: %d chars", len(tok))
	}
	tok2, _ := NewRandomToken(0)
	if tok == tok2 {
		t.Error("two CSPRNG draws identical")
	}
}

// TestConfigValidate covers the defense-in-depth bind rules mirrored from
// main.go's resolveAddr.
func TestConfigValidate(t *testing.T) {
	ok := []string{"127.0.0.1:8080", "[::1]:8080", "localhost:3000", ":8080", "0.0.0.0:8080"}
	bad := []string{"", "10.1.2.3:8080", "192.168.0.5:80", "127.0.0.1:0", "not-an-addr"}
	for _, a := range ok {
		if err := (Config{Addr: a}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", a, err)
		}
	}
	for _, a := range bad {
		if err := (Config{Addr: a}).Validate(); err == nil {
			t.Errorf("Validate(%q) = nil, want error", a)
		}
	}
}

// TestRecoverMiddlewareGeneric500 asserts panics never surface values or
// stack traces to the client (SECURITY_AND_OPSEC.md §1).
func TestRecoverMiddlewareGeneric500(t *testing.T) {
	app := &App{}
	mw := app.recoverMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("secret internal detail: password=hunter2")
	}))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic handler code = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "goroutine") {
		t.Errorf("panic detail leaked to client: %q", body)
	}
	if body != "internal error\n" {
		t.Errorf("body = %q, want generic", body)
	}
}

// TestHandleNotFoundAnonymousRedirect pins ARCHITECTURE.md §1 behaviour:
// anonymous 404s redirect home; authenticated users see the page.
func TestHandleNotFoundAnonymousRedirect(t *testing.T) {
	sm := newTestSessions(t)
	app := &App{sm: sm}

	rec := httptest.NewRecorder()
	sm.LoadAndSave(http.HandlerFunc(app.handleNotFound)).ServeHTTP(
		rec, httptest.NewRequest(http.MethodGet, "/typo", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous 404 = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/" {
		t.Errorf("redirect target = %q, want /", rec.Header().Get("Location"))
	}

	// Seed an authenticated session through the normal request path (scs has
	// no out-of-request setter), then replay its cookie against handleNotFound.
	pin := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), userIDKey, "u123")
		w.WriteHeader(http.StatusOK)
	}))
	recPin := httptest.NewRecorder()
	pin.ServeHTTP(recPin, httptest.NewRequest(http.MethodGet, "/pin", nil))
	cookie := sessionCookie(t, recPin, true)

	reqAuth := httptest.NewRequest(http.MethodGet, "/typo", nil)
	reqAuth.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	sm.LoadAndSave(http.HandlerFunc(app.handleNotFound)).ServeHTTP(rec2, reqAuth)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("authenticated 404 = %d, want 404", rec2.Code)
	}
}

// TestRouterWiring boots the full middleware chain via New() and exercises the
// happy path end-to-end: GET 200 with headers + session cookie, healthz
// exempt from limiting, POST without CSRF rejected.
func TestRouterWiring(t *testing.T) {
	app, err := New(Config{Addr: "127.0.0.1:8080", SecureCookies: false},
		Deps{StartedAt: time.Now()}, newTestSessions(t).Store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusSeeOther { // anonymous miss -> home
		t.Fatalf("GET / = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") != CSP {
		t.Error("CSP missing on router responses")
	}
	sessionCookie(t, rec, true)

	recH := httptest.NewRecorder()
	app.Handler().ServeHTTP(recH, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recH.Code != http.StatusOK || recH.Body.String() != "ok\n" {
		t.Errorf("healthz = %d %q, want 200 ok", recH.Code, recH.Body.String())
	}

	// POST without CSRF: with RDB nil the fail-closed limiter answers 503 for
	// writes (documented asymmetry); a GET miss still gets the anonymous 303.
	recP := httptest.NewRecorder()
	app.Handler().ServeHTTP(recP, httptest.NewRequest(http.MethodPost, "/anything", nil))
	if recP.Code != http.StatusServiceUnavailable {
		t.Errorf("POST with redis down = %d, want fail-closed 503", recP.Code)
	}

	// New refuses a nil store and a routable bind — no silent defaults.
	if _, err := New(Config{Addr: "127.0.0.1:8080"}, Deps{}, nil); err == nil {
		t.Error("New accepted nil session store")
	}
	if _, err := New(Config{Addr: "8.8.8.8:80"}, Deps{}, newTestSessions(t).Store); err == nil {
		t.Error("New accepted routable bind")
	}
}

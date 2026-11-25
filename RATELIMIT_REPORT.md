=========== 429 attribution + rate-limit key fix ===========

1) attributing the 429
   GET /login -> 200  Server=nginx
   body head: <!DOCTYPE html> <html lang="en"> <head> <meta charset="utf-8"> <meta name="viewport" content="width=device-width, initial-scale=1"> <meta name="color-scheme" co
   nginx limit_req present: False
   Go IP-keyed limiter candidate: internal/server/middleware_test.go

2) patching Go limiter key in internal/server/middleware_test.go
   no IP-key expression matched; printing limiter for manual review
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
	for _, name := range droppe

3) vet harness: retry-with-backoff on 429/503 + polite spider pacing
   _req retries 429/503 with backoff
   spider paces requests (0.15s)

4) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   OK   docker compose config --quiet

5) re-running vet triage

6) commit / push
   VERIFIED origin/main == HEAD

Applied:
 - Vet client retries transient 429/503
 - Spider paces requests

Manual:
 - Go limiter key not auto-patched — see report.
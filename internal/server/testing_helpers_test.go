package server

import (
	"testing"

	"github.com/alexedwards/scs/v2"
)

// routerWithoutRateLimitForTest builds the full production middleware chain
// MINUS the RateLimit hop (newApp's withRateLimit=false path — the ONLY
// sanctioned way to skip it; see router.go). httptest suites that exercise
// many POSTs in a tight loop use this so burst traffic never trips real
// windows; dedicated limiter behavior stays covered by the rate-limit tests.
// The session store is scs' in-memory backend with dev cookie settings,
// mirroring newTestSessions in middleware_test.go.
func routerWithoutRateLimitForTest(t *testing.T, cfg Config, deps Deps) *App {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8089" // never listened on; Config.Validate wants a sane value
	}
	a, err := newApp(cfg, deps, scs.New().Store, false)
	if err != nil {
		t.Fatalf("routerWithoutRateLimitForTest: %v", err)
	}
	return a
}

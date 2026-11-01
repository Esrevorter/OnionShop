package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"

	"github.com/example/onionshop/internal/store"
)

// Config carries everything main.go resolved from flags/env. Kept tiny and
// explicit: no functional options, per AGENTS.md §3 ("constructor + setters").
type Config struct {
	// Addr is the listener address. Must be loopback or a unix:// URL; anything
	// else is refused at construction (AGENTS.md §1, SECURITY_AND_OPSEC.md §4).
	Addr string
	// SecureCookies mirrors ONIONSHOP_SECURE_COOKIES (false only for local dev).
	SecureCookies bool
	// Sessions overrides DefaultSessionConfig() when non-nil.
	Sessions *SessionConfig
}

// Validate enforces the bind-address rule in code so a mistyped env var can
// never silently expose the app off-box.
func (c Config) Validate() error {
	if c.Addr == "" {
		return errors.New("server: empty listen address")
	}
	switch {
	case strings.HasPrefix(c.Addr, "unix://"):
		return nil
	case strings.HasPrefix(c.Addr, "127.0.0.1:"), strings.HasPrefix(c.Addr, "[::1]:"):
		return nil
	default:
		return errors.New("server: refusing to bind " + c.Addr + " (loopback or unix:// only)")
	}
}

// Deps are the wired subsystems. Phase 3+ handlers receive *App and pull their
// own dependencies off it — this is what keeps handler signatures uniform.
type Deps struct {
	Store     *store.FallbackStore // scs.CtxStore implementation
	Pool      interface{ Close() } // pgx pool handle kept for shutdown symmetry
	RDB       *redis.Client
	Sm        *scs.SessionManager
	Limiters  map[string]*RateLimiter
	BucketFor func(r *http.Request, userID string) string
	StartedAt time.Time
}

// App owns the router and its lifecycle.
type App struct {
	cfg    Config
	deps   Deps
	router chi.Router
	srv    *http.Server
	sm     *scs.SessionManager
}

// New wires middleware in the mandated order and returns a ready-to-Serve App.
//
// Middleware order (outermost first) — deliberate, do not reshuffle:
//  1. StripForwardedHeaders  — kill client-supplied forwarding lies before anything reads them
//  2. Recover                — chi panic catcher that emits our generic 500 (no stack leaks)
//  3. SecurityHeaders        — CSP et al. on every response, including errors
//  4. sessions.LoadAndSave   — establishes the session context used by everything below
//  5. AbsoluteExpiry         — enforce hard lifetime cap on top of scs idle timeout
//  6. CSRFLoad               — mint the double-submit token if absent
//  7. RateLimit              — identity-aware sliding windows (reads session/user keys)
//  8. CSRFProtect            — validate state-changing requests after throttling
//  9. routes                 — Phase 3 handlers attach here
func New(cfg Config, deps Deps) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Store == nil {
		return nil, errors.New("server: missing session store")
	}

	sc := DefaultSessionConfig()
	if cfg.SecureCookies {
		sc.SecureCookies = true
	} else {
		sc.SecureCookies = false
	}
	if deps.Sm == nil {
		deps.Sm = NewSessions(deps.Store, sc)
	}

	a := &App{cfg: cfg, deps: deps, sm: deps.Sm}
	r := chi.NewRouter()

	r.Use(StripForwardedHeaders())
	r.Use(a.recoverMiddleware())
	r.Use(SecurityHeaders())
	r.Use(deps.Sm.LoadAndSave)
	r.Use(AbsoluteExpiry(deps.Sm, sc.AbsoluteTimeout))
	r.Use(CSRFLoad(deps.Sm))
	r.Use(RateLimit(LimiterConfig{
		RDB:       deps.RDB,
		Buckets:   DefaultBuckets(),
		UserID:    func(req *http.Request) string { return CurrentUserID(req.Context(), deps.Sm) },
		SessionID: func(req *http.Request) string { return deps.Sm.Token(req.Context()) },
		BucketFor: bucketForRequest(deps.BucketFor),
	}))
	r.Use(CSRFProtect(deps.Sm))

	r.Get("/healthz", a.handleHealth)
	r.NotFound(a.handleNotFound)

	a.router = r
	a.srv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  75 * time.Second,
		// No MaxHeaderBytes surprise: default 1MB is fine behind nginx.
	}
	return a, nil
}

// Router exposes the chi router so later phases mount route groups without
// touching middleware order.
func (a *App) Router() chi.Router { return a.router }

// Handler returns the composed http.Handler (used by httptest suites).
func (a *App) Handler() http.Handler { return a.router }

// Serve runs the HTTP server until ctx is cancelled, then drains gracefully.
func (a *App) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if a.cfg.Network() == "unix" {
			ln, err := newUnixListener(a.cfg.Addr)
			if err != nil {
				errCh <- err
				return
			}
			errCh <- a.srv.Serve(ln)
			return
		}
		errCh <- a.srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return a.srv.Shutdown(shutdownCtx)
	}
}

// Network reports the listener kind derived from the address.
func (c Config) Network() string {
	if strings.HasPrefix(c.Addr, "unix://") {
		return "unix"
	}
	return "tcp"
}

// bucketForRequest maps path prefixes onto limiter buckets. The caller may
// supply an override (Phase 3 registers richer tables); the defaults encode
// the abuse surface we already know about.
func bucketForRequest(custom func(r *http.Request, userID string) string) func(*http.Request, string) string {
	if custom != nil {
		return custom
	}
	return func(r *http.Request, userID string) string {
		p := r.URL.Path
		switch {
		case p == "/healthz":
			return "" // liveness must never be throttled into a false alarm
		case strings.HasPrefix(p, "/login"), strings.HasPrefix(p, "/auth/"):
			return "login"
		case strings.HasPrefix(p, "/register"):
			return "register"
		case strings.HasPrefix(p, "/captcha"):
			return "captcha"
		case strings.HasPrefix(p, "/tickets/"), strings.HasPrefix(p, "/messages/"):
			if userID == "" {
				return "default"
			}
			return "message"
		default:
			return "default"
		}
	}
}

// recoverMiddleware converts panics into the generic 500 mandated by
// SECURITY_AND_OPSEC.md §1 (never a stack trace, never internals). We keep our
// own instead of chi's middleware.Recoverer because the stock one writes the
// panic to stderr with the value — values can carry PII.
func (a *App) recoverMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// Deliberately discard `rec`: logging it verbatim risks
					// leaking user input into journald (SECURITY §1).
					writeGenericError(w)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// handleHealth answers the compose/docker healthcheck. Body stays "ok\n":
// scripts/healthcheck.sh greps for it. It reports dependency reachability
// without exposing which dependency failed.
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := http.StatusOK
	body := "ok\n"

	if a.deps.RDB != nil {
		if err := a.deps.RDB.Ping(ctx).Err(); err != nil {
			status, body = http.StatusServiceUnavailable, "not ok\n"
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// handleNotFound renders the uniform 404. Anonymous visitors get redirected to
// "/" as specified (ARCHITECTURE.md §1); authenticated users see the page so
// they learn a typo cost them nothing operationally.
func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if CurrentUserID(r.Context(), a.sm) == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 not found\n"))
}

var _ = middleware.RequestID // reserved: request IDs arrive with structured logs in Phase 3

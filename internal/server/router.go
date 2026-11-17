package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
)

// Config carries everything main.go resolved from flags/env. Kept tiny and
// explicit: no functional options, per AGENTS.md §3 ("constructor + setters").
type Config struct {
	Addr          string
	UnixSocket    string
	SecureCookies bool
	Sessions      *SessionConfig
}

// Validate enforces the bind-address rule in code so a mistyped env var can
// never silently expose the app off-box.
func (c Config) Validate() error {
	if c.Addr == "" && c.UnixSocket == "" {
		return errors.New("server: empty listen address")
	}
	if c.Addr != "" {
		host, port, err := net.SplitHostPort(c.Addr)
		if err != nil {
			return fmt.Errorf("server: invalid listen address %q: %w", c.Addr, err)
		}
		switch host {
		case "127.0.0.1", "::1", "localhost", "", "0.0.0.0":
			// loopback: always fine; wildcard: only via the main.go container gate.
		default:
			return errors.New("server: refusing to bind " + c.Addr + " (loopback TCP only)")
		}
		if port == "0" {
			return errors.New("server: refusing to bind port 0")
		}
	}
	if c.UnixSocket != "" {
		if c.UnixSocket == "\x00" || len(c.UnixSocket) == 0 {
			return errors.New("server: invalid unix socket path")
		}
	}
	return nil
}

// Deps carries the wired subsystems.
type Deps struct {
	Pool      interface{ Close(); Ping(context.Context) error }
	RDB       *redis.Client
	Sm        *scs.SessionManager
	Limiters  map[string]*RateLimiter
	BucketFor func(r *http.Request, userID string) string
	StartedAt time.Time
}

// App owns the router and its lifecycle.
type App struct {
	cfg       Config
	deps      Deps
	router    chi.Router
	srv       *http.Server
	sm        *scs.SessionManager
	auth      AuthStore
	captcha   *CaptchaService
	linker    SessionLinker
	invalid   SessionInvalidator
	vendor    VendorStore
	templates map[string]*template.Template
}

func New(cfg Config, deps Deps, sessionStore scs.Store) (*App, error) {
	return newApp(cfg, deps, sessionStore, true)
}

func newApp(cfg Config, deps Deps, sessionStore scs.Store, withRateLimit bool) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if sessionStore == nil {
		return nil, errors.New("server: missing session store")
	}

	sc := DefaultSessionConfig()
	sc.SecureCookies = cfg.SecureCookies
	if deps.Sm == nil {
		deps.Sm = NewSessions(sessionStore, sc)
	}

	a := &App{cfg: cfg, deps: deps, sm: deps.Sm}
	r := chi.NewRouter()

	r.Use(StripForwardedHeaders())
	r.Use(a.recoverMiddleware())
	r.Use(SecurityHeaders())
	r.Use(deps.Sm.LoadAndSave)
	r.Use(AbsoluteExpiry(deps.Sm, sc.AbsoluteTimeout))
	r.Use(CSRFLoad(deps.Sm))
	if withRateLimit {
		r.Use(RateLimit(LimiterConfig{
			RDB:       deps.RDB,
			Buckets:   DefaultBuckets(),
			UserID:    func(req *http.Request) string { return CurrentUserID(req.Context(), deps.Sm) },
			SessionID: func(req *http.Request) string { return deps.Sm.Token(req.Context()) },
			BucketFor: bucketForRequest(deps.BucketFor),
		}))
	}
	r.Use(CSRFProtect(deps.Sm))

	r.Get("/healthz", a.handleHealth)
	// CAPTCHA images ride the same router as the forms that embed them.
	// The id contains a dot (nonce.mac), so the route is keyed on the whole
	// segment ({id}) with NO trailing literal: a "{id}.png" pattern makes chi
	// capture "nonce.mac.png" and the lookup misses. The handler sets
	// Content-Type: image/png explicitly, so the extension is unnecessary.
	r.Get("/captcha/{id}", a.handleCaptchaImage)
	r.NotFound(a.handleNotFound)

	a.router = r
	a.srv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  75 * time.Second,
	}
	return a, nil
}

func (a *App) Router() chi.Router { return a.router }
func (a *App) Handler() http.Handler { return a.router }

func (a *App) Serve(ctx context.Context) error {
	var (
		errCh    = make(chan error, 1)
		listener net.Listener
		err      error
	)
	if a.cfg.UnixSocket != "" {
		if fi, statErr := os.Stat(a.cfg.UnixSocket); statErr == nil && fi.Mode()&os.ModeSocket != 0 {
			if rmErr := os.Remove(a.cfg.UnixSocket); rmErr != nil {
				return fmt.Errorf("server: remove stale socket: %w", rmErr)
			}
		}
		listener, err = net.Listen("unix", a.cfg.UnixSocket)
	} else {
		listener, err = net.Listen("tcp", a.cfg.Addr)
	}
	if err != nil {
		return err
	}
	a.srv.Addr = a.cfg.UnixSocket
	if a.srv.TLSConfig != nil {
		a.srv.TLSConfig = nil
	}

	go func() {
		errCh <- a.srv.Serve(listener)
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

func bucketForRequest(custom func(*http.Request, string) string) func(*http.Request, string) string {
	if custom != nil {
		return custom
	}
	return func(r *http.Request, userID string) string {
		p := r.URL.Path
		switch {
		case p == "/healthz":
			return ""
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

func (a *App) recoverMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					writeGenericError(w)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := http.StatusOK
	body := "ok\n"

	if a.deps.Pool == nil {
		status, body = http.StatusServiceUnavailable, "not ok\n"
	} else if pinger, ok := a.deps.Pool.(interface{ Ping(context.Context) error }); ok {
		if err := pinger.Ping(ctx); err != nil {
			status, body = http.StatusServiceUnavailable, "not ok\n"
		}
	}

	if status == http.StatusOK && a.deps.RDB != nil {
		if err := a.deps.RDB.Ping(ctx).Err(); err != nil {
			status, body = http.StatusServiceUnavailable, "not ok\n"
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if CurrentUserID(r.Context(), a.sm) == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 not found\n"))
}

var _ chi.Router
var _ = middleware.RequestID

// Command onionshop is the OnionShop HTTP server and background workers.
// OpSec policy (SECURITY_AND_OPSEC.md §4): the clear-text listener must never
// be reachable off-box. On bare-metal/systemd deployments that means loopback
// only — the Tor daemon fronts it as a v3 hidden service. In containerized
// deployments (docker-compose) the app container carries NET_NEVER so its
// network namespace has no route to the outside world: binding there is
// equally contained, and nginx reaches it over the internal compose network.
// See allowContainerBind below.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/example/onionshop/internal/server"
	"github.com/example/onionshop/internal/store"
)

const defaultAddr = "127.0.0.1:8080"

// netNeverEnv is the opt-in marker set by docker-compose (and any other
// sandbox that provably removes off-box reachability from this process's
// network namespace). Docker itself sets no env var to advertise NET_NEVER,
// so the deployment file must declare it explicitly; the operator opts in
// rather than the guard being silently weakened.
const netNeverEnv = "ONIONSHOP_NET_NEVER"

// allowContainerBind reports whether binding outside loopback is safe in the
// current environment. It returns true only when ONIONSHOP_NET_NEVER is set
// to a truthy value ("1", "true", "yes"): the compose stack sets it on the
// app container, which runs with `network_mode: "container:tor"`-equivalent
// isolation (NET_NEVER network + internal-only bridge), so a non-loopback
// bind cannot be reached off-box. Everywhere else (bare metal, systemd,
// misconfigured hosts) the strict localhost-only rule still applies.
func allowContainerBind() bool {
	switch strings.ToLower(envOr(netNeverEnv, "")) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// resolveAddr validates the -addr/ONIONSHOP_ADDR target against the OpSec
// bind policy and returns the address to listen on. It is a pure function so
// tests can cover every branch without booting the server.
func resolveAddr(raw string) (string, error) {
	if raw == "" {
		raw = defaultAddr
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("invalid -addr %q: %w", raw, err)
	}
	loopback := host == "127.0.0.1" || host == "::1" || host == "localhost"
	if loopback {
		return net.JoinHostPort(host, port), nil
	}
	if !allowContainerBind() {
		// Refuse to run if misconfigured toward a public interface.
		return "", fmt.Errorf("refusing to bind to %q: OnionShop must listen on localhost only (SECURITY_AND_OPSEC.md; set %s=1 inside a NET_NEVER container to override)", host, netNeverEnv)
	}
	// Contained-mode normalization: ":8080" or "0.0.0.0:8080" both mean
	// "reachable from my own network namespace". Docker's internal bridge
	// delivers packets addressed to the container's eth0 IP, so we bind the
	// wildcard here — but ONLY when ONIONSHOP_NET_NEVER=1 proves the
	// namespace has no off-box route. The choice stays explicit and loud.
	if host == "" || host == "0.0.0.0" {
		log.Printf("%s=1: binding all interfaces of this contained network namespace on port %s (no off-box route expected — verify with `docker inspect`)", netNeverEnv, port)
		return net.JoinHostPort("0.0.0.0", port), nil
	}
	log.Printf("%s=1: allowing non-loopback bind %q (contained network namespace)", netNeverEnv, net.JoinHostPort(host, port))
	return net.JoinHostPort(host, port), nil
}

func main() {
	addr := flag.String("addr", envOr("ONIONSHOP_ADDR", ""),
		"TCP listen address (loopback only, unless ONIONSHOP_NET_NEVER=1 in a contained network namespace; defaults to $ONIONSHOP_ADDR, then "+defaultAddr+")")
	flag.Parse()

	target, err := resolveAddr(*addr)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := server.Deps{StartedAt: time.Now().UTC()}

	// Infrastructure is best-effort at this stage: when Postgres/Redis are not
	// configured or reachable we log the degradation and let the router's
	// health endpoint report overall status (Phase 2 wiring, see TASKS.md).
	var pool *pgxpool.Pool
	if p, err := store.OpenDBFromEnv(ctx); err == nil {
		pool = p
		deps.Pool = pool
		defer pool.Close() // pgxpool.Pool.Close() returns no value
	} else {
		log.Printf("postgres unavailable (degraded mode): %v", err)
	}
	var rdb *redis.Client
	if c, err := store.OpenRedisFromEnv(ctx); err == nil {
		rdb = c
		deps.RDB = rdb
		defer func() { _ = rdb.Close() }() // shutdown-time close; error is not actionable
	} else {
		log.Printf("redis unavailable (degraded mode): %v", err)
	}

	// Session backend selection: Redis fast path + Postgres durable mirror
	// when either is configured; in-memory only when neither is available
	// (dev/test — sessions cannot survive restarts, so production compose
	// always wires Postgres).
	var sessionStore scs.Store
	switch {
	case pool != nil || rdb != nil:
		sessionStore = store.NewFallbackStore(rdb, store.NewSessionFallback(pool))
	default:
		log.Print("no session backend configured: using in-memory sessions (dev only)")
		sessionStore = scs.New().Store
	}

	app, err := server.New(server.Config{
		Addr:          target,
		SecureCookies: envOr("ONIONSHOP_SECURE_COOKIES", "true") != "false",
	}, deps, sessionStore)
	if err != nil {
		log.Fatalf("server setup failed: %v", err)
	}

	go func() {
		if err := app.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("server error: %v", err)
		}
	}()
	// Bind policy was already vetted by resolveAddr above: loopback, or a
	// contained-namespace bind under ONIONSHOP_NET_NEVER=1.
	log.Printf("onionshop listening on %s (TCP; bind policy enforced by resolveAddr)", target)

	<-ctx.Done()
	log.Print("shutdown signal received")
}

// envOr returns the value of environment variable key, or fallback when unset
// or empty. Used for every ONIONSHOP_* setting so flags/env stay symmetric.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

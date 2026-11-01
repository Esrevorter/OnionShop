// Command onionshop is the OnionShop HTTP server and background workers.
// It MUST bind only to 127.0.0.1 (see SECURITY_AND_OPSEC.md): the Tor daemon
// fronts it as a v3 hidden service; nothing else may reach it directly.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/example/onionshop/internal/server"
	"github.com/example/onionshop/internal/store"
)

const defaultAddr = "127.0.0.1:8080"

func main() {
	addr := flag.String("addr", envOr("ONIONSHOP_ADDR", ""),
		"TCP listen address (loopback only; defaults to $ONIONSHOP_ADDR, then "+defaultAddr+")")
	flag.Parse()

	target := *addr
	if target == "" {
		target = defaultAddr
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		log.Fatalf("invalid -addr %q: %v", target, err)
	}
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		// Refuse to run if misconfigured toward a public interface.
		log.Fatalf("refusing to bind to %q: OnionShop must listen on localhost only (SECURITY_AND_OPSEC.md)", host)
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
		defer pool.Close()
	} else {
		log.Printf("postgres unavailable (degraded mode): %v", err)
	}
	var rdb *redis.Client
	if c, err := store.OpenRedisFromEnv(ctx); err == nil {
		rdb = c
		deps.RDB = rdb
		defer rdb.Close()
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
	log.Printf("onionshop listening on %s (localhost-only TCP)", target)

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

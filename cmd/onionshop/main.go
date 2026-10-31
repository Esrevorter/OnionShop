// Command onionshop is the OnionShop HTTP server and background workers.
// It MUST bind only to 127.0.0.1 (see SECURITY_AND_OPSEC.md): the Tor daemon
// fronts it as a v3 hidden service; nothing else may reach it directly.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const defaultAddr = "127.0.0.1:8080"

func main() {
	addr := flag.String("addr", envOr("ONIONSHOP_ADDR", defaultAddr), "listen address (must be localhost)")
	flag.Parse()

	if host, _, err := net.SplitHostPort(*addr); err != nil {
		log.Fatalf("invalid -addr %q: %v", *addr, err)
	} else if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		// Refuse to run if misconfigured toward a public interface.
		log.Fatalf("refusing to bind to %q: OnionShop must listen on localhost only (SECURITY_AND_OPSEC.md)", host)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	// Security headers middleware, chi router, sessions, CSRF, rate limiting
	// are wired in here as Phase 2 lands (see TASKS.md).

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("onionshop listening on %s (localhost-only)", *addr)

	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

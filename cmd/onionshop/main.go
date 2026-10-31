// Command onionshop is the OnionShop HTTP server and background workers.
// It MUST bind only to 127.0.0.1 (see SECURITY_AND_OPSEC.md): the Tor daemon
// fronts it as a v3 hidden service; nothing else may reach it directly.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultAddr   = "127.0.0.1:8080"
	defaultSocket = "/run/onionshop/app.sock"
)

var secureCookie = os.Getenv("ONIONSHOP_SECURE_COOKIES") != "false" // true unless explicitly disabled for local dev

func main() {
	addr := flag.String("addr", "", "TCP listen address (must be loopback; use only when no UNIX socket is configured)")
	socket := flag.String("unix-socket", "", "path to the UNIX socket nginx proxies to (recommended; see SECURITY_AND_OPSEC.md)")
	flag.Parse()

	// Env fallback keeps docker-compose simple (it passes ONIONSHOP_ADDR/
	// ONIONSHOP_SOCKET); explicit flags always win over the environment.
	if *addr == "" && *socket == "" {
		*addr = os.Getenv("ONIONSHOP_ADDR")
		*socket = os.Getenv("ONIONSHOP_SOCKET")
	}
	// Nothing configured: safe default — the production UNIX socket
	// (/run/onionshop/app.sock), never a public TCP bind.
	if *addr == "" && *socket == "" {
		*socket = defaultSocket
	}

	// Exactly one listener must be configured. Production uses the UNIX socket
	// (no TCP hop between nginx and app); loopback TCP is permitted as a
	// fallback for containerised deployments where socket sharing is awkward.
	if (*addr == "") == (*socket == "") {
		log.Fatalf("configure exactly one of -addr or -unix-socket (env ONIONSHOP_ADDR / ONIONSHOP_SOCKET); got addr=%q socket=%q", *addr, *socket)
	}
	if *addr != "" {
		host, _, err := net.SplitHostPort(*addr)
		if err != nil {
			log.Fatalf("invalid -addr %q: %v", *addr, err)
		}
		if host != "127.0.0.1" && host != "::1" && host != "localhost" {
			// Refuse to run if misconfigured toward a public interface.
			log.Fatalf("refusing to bind to %q: OnionShop must listen on localhost only (SECURITY_AND_OPSEC.md)", host)
		}
	}

	_ = defaultAddr // documented loopback fallback; used by docker-compose via ONIONSHOP_ADDR

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	// Security headers middleware, chi router, sessions, CSRF, rate limiting
	// are wired in here as Phase 2 lands (see TASKS.md).

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	if *socket != "" {
		ln, err := listenUnixSocket(*socket)
		if err != nil {
			log.Fatalf("cannot listen on %s: %v", *socket, err)
		}
		go func() { errCh <- srv.Serve(ln) }()
		log.Printf("onionshop listening on UNIX socket %s (nginx-only upstream)", *socket)
	} else {
		srv.Addr = *addr
		go func() { errCh <- srv.ListenAndServe() }()
		log.Printf("onionshop listening on %s (localhost-only)", *addr)
	}

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
	if *socket != "" {
		// Remove the stale socket so nginx gets a clean ECONNREFUSED until restart.
		os.Remove(*socket)
	}
}

// listenUnixSocket creates (or reuses) the UNIX socket nginx proxies to.
// Stale sockets from crashed runs are removed; permissions are 0770 so that
// both the app user and the nginx group can reach it (SECURITY_AND_OPSEC.md).
func listenUnixSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir socket dir: %w", err)
	}
	// Reject symlinks planted by another local process.
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink; refusing", path)
		}
		if fi.Mode()&os.ModeSocket != 0 {
			if c, derr := net.DialTimeout("unix", path, 500*time.Millisecond); derr == nil {
				c.Close()
				return nil, fmt.Errorf("%s is in use by a live listener", path)
			}
			os.Remove(path) // stale socket from a previous crash
		} else {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
	}
	old := syscall.Umask(0o007)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o770); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, nil
}

// sessionCookie builds the hardened session cookie described in
// SECURITY_AND_OPSEC.md: HttpOnly + SameSite=Strict + Secure (Secure may be
// relaxed only for local development via ONIONSHOP_SECURE_COOKIES=false).
func sessionCookie(name, value string, maxAgeSec int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secureCookie,
		MaxAge:   maxAgeSec,
	}
}

// randomToken returns 32 cryptographically-random bytes, base64url-encoded —
// used for session IDs and CSRF tokens (never derived from request data).
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("crypto/rand unavailable: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken stores only salted SHA-256 hashes of session identifiers (RAM-only
// in Redis; plaintext IDs must never be persisted or logged).
func hashToken(token string) string {
	sum := sha256.Sum256([]byte("onionshop-session-v1|" + token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// splitSecret parses an env var like "id:secret" (used for BTCPay internal API keys).
func splitSecret(v string) (string, string, bool) {
	parts := strings.SplitN(v, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

// runHealthcheck implements `onionshop healthcheck`, the container healthcheck
// entrypoint. It probes GET /healthz over the SAME transport nginx uses: the
// UNIX socket when ONIONSHOP_UNIX is set, otherwise loopback TCP.
//
// Why this exists: App.Serve binds exactly one listener (UNIX or TCP), so the
// previous `wget http://127.0.0.1:8080/healthz` probe could never succeed in the
// compose deployment — the app was permanently unhealthy and nginx, which
// gates on service_healthy, was never created.
//
// Exit 0 = healthy, 1 = unhealthy. Silent: Docker records the exit code, and
// any output would be retained in `docker inspect` health logs.
func runHealthcheck() int {
	const timeout = 3 * time.Second

	var (
		client *http.Client
		url    string
	)
	if sock := envOr("ONIONSHOP_UNIX", ""); sock != "" {
		client = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				// The URL's host:port is ignored by this dialer; every
				// request goes to the socket path regardless.
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sock)
				},
			},
		}
		url = "http://unix/healthz"
	} else {
		client = &http.Client{Timeout: timeout}
		url = "http://" + envOr("ONIONSHOP_ADDR", defaultAddr) + "/healthz"
	}

	resp, err := client.Get(url)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

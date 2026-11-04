// Package cron hosts OnionShop's background workers (ARCHITECTURE.md §9).
// Today: the top-vendors leaderboard snapshot that the homepage widget reads
// — recomputed on a fixed interval into top_vendors_snapshot, never per
// request. Phase 5 adds the escrow auto-release pass here; keep each worker
// in its own file and reuse Leaderboard's ticker/shutdown skeleton.
package cron

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Refreshable is the slice of the catalog store this worker needs
// (*server.PGCatalogStore satisfies it; tests substitute a fake — no
// Postgres in CI, AGENTS.md). Kept as a one-method interface so the cron
// package never imports the server package (no import cycle, no wiring
// coupling beyond the DB surface).
type Refreshable interface {
	// CatalogRefreshTopVendors rewrites the leaderboard snapshot atomically
	// and returns how many rows landed in the new board.
	CatalogRefreshTopVendors(ctx context.Context, limit int) (int, error)
}

// DefaultInterval is the leaderboard refresh cadence. The board is a display
// convenience, not an authority (payouts never read it), so hourly staleness
// is acceptable and keeps the aggregate query off every page view.
const DefaultInterval = time.Hour

// Leaderboard periodically refreshes the top-vendors snapshot. Zero value is
// not usable; construct with NewLeaderboard. Run blocks until the context is
// cancelled; Stop is safe to call from another goroutine (or via context
// cancellation alone — both paths converge).
type Leaderboard struct {
	store    Refreshable
	interval time.Duration
	limit    int

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// NewLeaderboard wires the worker. interval <= 0 falls back to
// DefaultInterval; limit <= 0 lets the store apply its own default board
// size (server.DefaultLeaderboardSize).
func NewLeaderboard(store Refreshable, interval time.Duration, limit int) *Leaderboard {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Leaderboard{store: store, interval: interval, limit: limit, done: make(chan struct{})}
}

// Run refreshes once immediately (a fresh boot should not stare at an empty
// widget until the first tick), then on every interval until ctx is done.
// Errors are logged and skipped — one failed refresh must never kill the
// worker or take the HTTP listener with it.
func (l *Leaderboard) Run(ctx context.Context) {
	defer close(l.done)
	if l.store == nil {
		log.Print("cron: leaderboard worker disabled (no store wired)")
		return
	}
	tick := time.NewTicker(l.interval)
	defer tick.Stop()
	l.refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			l.refresh(ctx)
		}
	}
}

// refresh performs one snapshot rewrite. A cancelled/truncated context mid
// write leaves the previous board intact: the store commits DELETE+INSERTs
// in one transaction (catalog_store.go), so readers never see a half-board.
func (l *Leaderboard) refresh(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	n, err := l.store.CatalogRefreshTopVendors(rctx, l.limit)
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// Shutdown racing a refresh is routine; loud log would be noise.
		return
	case err != nil:
		log.Printf("cron: leaderboard refresh failed: %v", err)
	default:
		log.Printf("cron: leaderboard refreshed (%d vendors)", n)
	}
}

// Wait blocks until Run has returned (used by main.go shutdown ordering and
// tests). Safe to call only after Run was started (goroutine) — calling it
// before Run ever ran deadlocks by design; cmd wiring always spawns Run.
func (l *Leaderboard) Wait() { <-l.done }

var _ = sync.Mutex{} // reserved: Stop()/liveness hooks land with Phase 5 workers

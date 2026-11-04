package cron

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeStore struct {
	calls atomic.Int64
	limit atomic.Int64
	err   error
}

func (f *fakeStore) CatalogRefreshTopVendors(_ context.Context, limit int) (int, error) {
	f.calls.Add(1)
	f.limit.Store(int64(limit))
	if f.err != nil {
		return 0, f.err
	}
	return 3, nil
}

func TestLeaderboardRunsImmediatelyAndStopsOnCancel(t *testing.T) {
	fs := &fakeStore{}
	lb := NewLeaderboard(fs, time.Hour, 5) // hourly: only the immediate pass fires
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { lb.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for fs.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("initial refresh never ran")
		case <-time.After(time.Millisecond):
		}
	}
	if got := fs.limit.Load(); got != 5 {
		t.Errorf("limit = %d, want 5", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	lb.Wait() // must not block after Run returned
}

func TestLeaderboardErrorDoesNotKillWorker(t *testing.T) {
	fs := &fakeStore{err: errors.New("boom")}
	lb := NewLeaderboard(fs, 10*time.Millisecond, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { lb.Run(ctx); close(done) }()
	deadline := time.After(2 * time.Second)
	for fs.calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("worker stopped ticking after an error")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestLeaderboardNilStoreExitsCleanly(t *testing.T) {
	lb := NewLeaderboard(nil, time.Hour, 0)
	done := make(chan struct{})
	go func() { lb.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil-store worker should return immediately")
	}
}

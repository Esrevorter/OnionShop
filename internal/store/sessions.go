package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Session table layout used by the scs Postgres fallback store. The existing
// db/migrations/0001 `sessions` table predates scs (bytea token + JSONB blob +
// absolute_stop); rather than break it, migration 0005 adds the `expiry`
// column scs needs plus a partial unique index so only live rows are
// constrained, and widens `id` to TEXT (scs tokens are base64 strings).
const (
	scsExpiryCol = "expiry" // TIMESTAMPTZ added by 0005; drives scs expiry semantics
)

// ErrStoreUnavailable marks failures coming from a dead backing store; callers
// wrap them for logging but must never leak the detail to the client.
var ErrStoreUnavailable = errors.New("store: backend unavailable")

// SessionFallback is the durable Postgres session store (scs CtxStore). It is
// the safety net behind Redis: if Redis restarts (dataset is RAM-only by
// design) sessions survive because every commit also lands in Postgres.
type SessionFallback struct {
	pool *pgxpool.Pool
}

// NewSessionFallback adapts the pgx pool to scs' CtxStore interface.
func NewSessionFallback(pool *pgxpool.Pool) *SessionFallback {
	return &SessionFallback{pool: pool}
}

// Find loads a session row. Expired or missing tokens are "not found" without
// an error (scs treats err as a system failure only).
func (f *SessionFallback) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	var data []byte
	err := f.pool.QueryRow(ctx,
		`SELECT data FROM sessions
		  WHERE id = $1 AND expires_at > now() AND current_timestamp < `+scsExpiryCol,
		token).Scan(&data)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return data, true, nil
}

// Commit upserts the session row. absolute_stop keeps the original hard
// deadline on first write; subsequent refreshes only move the idle expiry.
func (f *SessionFallback) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	_, err := f.pool.Exec(ctx,
		`INSERT INTO sessions (id, data, expires_at, `+scsExpiryCol+`, absolute_stop)
		 VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (id) DO UPDATE
		    SET data = EXCLUDED.data,
		        expires_at = EXCLUDED.expires_at,
		        `+scsExpiryCol+` = EXCLUDED.`+scsExpiryCol+`,
		        absolute_stop = LEAST(sessions.absolute_stop, EXCLUDED.absolute_stop)`,
		token, data, expiry, expiry)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

// Delete removes the session row (logout / kill).
func (f *SessionFallback) DeleteCtx(ctx context.Context, token string) error {
	_, err := f.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, token)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

// Cleanup drops expired rows; wired into the maintenance cron.
func (f *SessionFallback) Cleanup(ctx context.Context) error {
	_, err := f.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

// ActiveUserCount reports how many live sessions belong to authenticated users
// (used by the staff dashboard; no PII leaves this function).
func (f *SessionFallback) ActiveUserCount(ctx context.Context) (int64, error) {
	var n int64
	err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id IS NOT NULL AND expires_at > now()`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return n, nil
}

// scs.Store non-context methods: the SessionManager always calls the Ctx
// variants when the store implements CtxStore; these exist so the types also
// satisfy the plain Store interface (and remain usable from code paths that
// hold no request context, e.g. the maintenance cron).

// Find is the context-free variant of FindCtx.
func (f *SessionFallback) Find(token string) ([]byte, bool, error) {
	return f.FindCtx(context.Background(), token)
}

// Commit is the context-free variant of CommitCtx.
func (f *SessionFallback) Commit(token string, b []byte, expiry time.Time) error {
	return f.CommitCtx(context.Background(), token, b, expiry)
}

// Delete is the context-free variant of DeleteCtx.
func (f *SessionFallback) Delete(token string) error {
	return f.DeleteCtx(context.Background(), token)
}

// Find is the context-free variant of FindCtx.
func (s *FallbackStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

// Commit is the context-free variant of CommitCtx.
func (s *FallbackStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

// Delete is the context-free variant of DeleteCtx.
func (s *FallbackStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

// compile-time proof that both stores satisfy scs' context-aware interface.
var (
	_ scs.CtxStore = (*SessionFallback)(nil)
	_ scs.CtxStore = (*FallbackStore)(nil)
)

// FallbackStore tries Redis first (fast path) and always mirrors commits to
// Postgres so a Redis restart/eviction cannot log everyone out or resurrect
// stale carts (ARCHITECTURE.md §2). Reads prefer Redis and fall back to
// Postgres when the key is gone.
type FallbackStore struct {
	rdb      *redis.Client
	fallback *SessionFallback
	// prefix namespaces session keys away from rate-limit/cart keys in the
	// same Redis instance.
	prefix string
}

// NewFallbackStore builds the composite store. Either backend may be nil only
// in tests; production wiring requires both.
func NewFallbackStore(rdb *redis.Client, fb *SessionFallback) *FallbackStore {
	return &FallbackStore{rdb: rdb, fallback: fb, prefix: "sess:"}
}

func (s *FallbackStore) key(token string) string { return s.prefix + token }

// Find reads Redis, then Postgres. A Redis system error degrades to the
// fallback instead of failing the request outright.
func (s *FallbackStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	if s.rdb != nil {
		b, err := s.rdb.Get(ctx, s.key(token)).Bytes()
		switch {
		case err == nil:
			return b, true, nil
		case errors.Is(err, redis.Nil):
			// miss: consult Postgres below
		default:
			// Redis down: degrade quietly (no PII in logs beyond the token hash).
		}
	}
	if s.fallback == nil {
		return nil, false, nil
	}
	return s.fallback.FindCtx(ctx, token)
}

// Commit writes Postgres first (durability), then Redis (speed). If Redis
// rejects (e.g. OOM under noeviction) the session still exists in Postgres.
func (s *FallbackStore) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	if s.fallback != nil {
		if err := s.fallback.CommitCtx(ctx, token, b, expiry); err != nil {
			return err
		}
	}
	if s.rdb != nil {
		ttl := time.Until(expiry)
		if ttl <= 0 {
			return nil // already expired upstream; nothing to cache
		}
		if err := s.rdb.Set(ctx, s.key(token), b, ttl).Err(); err != nil {
			return fmt.Errorf("%w: redis mirror: %v", ErrStoreUnavailable, err)
		}
	}
	return nil
}

// Delete removes from both stores. Missing keys are a no-op per scs contract.
func (s *FallbackStore) DeleteCtx(ctx context.Context, token string) error {
	var firstErr error
	if s.rdb != nil {
		if err := s.rdb.Del(ctx, s.key(token)).Err(); err != nil && !errors.Is(err, redis.Nil) {
			firstErr = fmt.Errorf("%w: redis delete: %v", ErrStoreUnavailable, err)
		}
	}
	if s.fallback != nil {
		if err := s.fallback.DeleteCtx(ctx, token); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Touch copies a Redis-missing session back up from Postgres so hot sessions
// re-enter the fast path after a flush. Called by the maintenance ticker.
func (s *FallbackStore) Touch(ctx context.Context, token string) error {
	if s.rdb == nil || s.fallback == nil {
		return nil
	}
	exists, err := s.rdb.Exists(ctx, s.key(token)).Result()
	if err != nil || exists == 1 {
		return errToStoreErr(err)
	}
	b, found, err := s.fallback.FindCtx(ctx, token)
	if err != nil || !found {
		return err
	}
	// Re-commit with a conservative TTL; scs will refresh it on next write.
	return s.rdb.Set(ctx, s.key(token), b, 15*time.Minute).Err()
}

func errToStoreErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
}

// TouchAll re-inserts live Postgres sessions into Redis after a flush or
// restart (bounded batch; called by the maintenance cron in later phases).
func (s *FallbackStore) TouchAll(ctx context.Context, limit int) (int, error) {
	if s.rdb == nil || s.fallback == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.fallback.pool.Query(ctx,
		`SELECT id, data, LEAST(expires_at, `+scsExpiryCol+`) FROM sessions
		  WHERE expires_at > now() ORDER BY updated_at DESC NULLS LAST LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var (
			token   string
			data    []byte
			expires time.Time
		)
		if err := rows.Scan(&token, &data, &expires); err != nil {
			return n, fmt.Errorf("%w: scan: %v", ErrStoreUnavailable, err)
		}
		ttl := time.Until(expires)
		if ttl <= 0 {
			continue
		}
		if err := s.rdb.Set(ctx, s.key(token), data, ttl).Err(); err != nil {
			return n, fmt.Errorf("%w: redis warm: %v", ErrStoreUnavailable, err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return n, nil
}

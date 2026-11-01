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

// Table/column contract with db/migrations/0001_core_schema.up.sql +
// db/migrations/0005_sessions_scs.{up,down}.sql. The legacy 0001 table stores
// the token as BYTEA and the payload as JSONB; scs needs a text token and an
// opaque gob blob, so 0005 rewrites those two columns and adds `updated_at`.
//
// Expiry semantics (deliberately simplified vs. the original draft, which
// invented an `expiry` column that 0005 never created — every query would have
// failed with "column expiry does not exist"):
//   - expires_at    = scs idle expiry, rewritten on every commit;
//   - absolute_stop = hard lifetime cap, set once at creation and clamped with
//     LEAST() so an idle refresh can never extend a session's life.
//
// A row is live only when BOTH are in the future.
const (
	sessionSelectSQL = `SELECT data FROM sessions
 WHERE id = $1 AND expires_at > now() AND absolute_stop > now()`

	sessionCommitSQL = `INSERT INTO sessions (id, data, expires_at, absolute_stop, updated_at)
 VALUES ($1, $2, $3, $4, now())
 ON CONFLICT (id) DO UPDATE
     SET data          = EXCLUDED.data,
         expires_at    = EXCLUDED.expires_at,
         absolute_stop = LEAST(sessions.absolute_stop, EXCLUDED.absolute_stop),
         updated_at    = now()`

	sessionDeleteSQL = `DELETE FROM sessions WHERE id = $1`

	sessionCleanupSQL = `DELETE FROM sessions WHERE expires_at < now()`

	sessionCountSQL = `SELECT count(*) FROM sessions
 WHERE user_id IS NOT NULL AND expires_at > now() AND absolute_stop > now()`

	sessionTouchableSQL = `SELECT id, data, expires_at, absolute_stop FROM sessions
 WHERE expires_at > now() AND absolute_stop > now()
 ORDER BY updated_at DESC NULLS LAST
 LIMIT $1`

	sessionSetUserSQL = `UPDATE sessions SET user_id = $2, updated_at = now() WHERE id = $1`
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
	err := f.pool.QueryRow(ctx, sessionSelectSQL, token).Scan(&data)
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
	_, err := f.pool.Exec(ctx, sessionCommitSQL, token, data, expiry, expiry)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

// Delete removes the session row (logout / kill).
func (f *SessionFallback) DeleteCtx(ctx context.Context, token string) error {
	_, err := f.pool.Exec(ctx, sessionDeleteSQL, token)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

// Cleanup drops expired rows; wired into the maintenance cron.
func (f *SessionFallback) Cleanup(ctx context.Context) error {
	_, err := f.pool.Exec(ctx, sessionCleanupSQL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

// ActiveUserCount reports how many live sessions belong to authenticated users
// (used by the staff dashboard; no PII leaves this function).
func (f *SessionFallback) ActiveUserCount(ctx context.Context) (int64, error) {
	var n int64
	err := f.pool.QueryRow(ctx, sessionCountSQL).Scan(&n)
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

// AllCtx returns every live session payload, keyed by token, so scs managers
// with an IterableCtxStore can support Iterate (used later by the "kill all
// sessions for user" admin action). Redis is not iterable here (no SCAN-based
// guarantee under noeviction), so this reads the durable Postgres copy — which
// is authoritative because Commit writes Postgres first.
func (s *FallbackStore) AllCtx(ctx context.Context) (map[string][]byte, error) {
	if s.fallback == nil {
		return map[string][]byte{}, nil
	}
	rows, err := s.fallback.pool.Query(ctx, sessionTouchableSQL, 5000)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var (
			token         string
			data          []byte
			expires, stop time.Time
		)
		if err := rows.Scan(&token, &data, &expires, &stop); err != nil {
			return nil, fmt.Errorf("%w: scan: %v", ErrStoreUnavailable, err)
		}
		if time.Until(expires) <= 0 || time.Until(stop) <= 0 {
			continue
		}
		out[token] = data
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return out, nil
}

// All is the context-free variant of AllCtx.
func (s *FallbackStore) All() (map[string][]byte, error) {
	return s.AllCtx(context.Background())
}

// compile-time proof that both stores satisfy scs' context-aware interface.
var (
	_ scs.CtxStore         = (*SessionFallback)(nil)
	_ scs.IterableCtxStore = (*FallbackStore)(nil)
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
	rows, err := s.fallback.pool.Query(ctx, sessionTouchableSQL, limit)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var (
			token         string
			data          []byte
			expires, stop time.Time
		)
		if err := rows.Scan(&token, &data, &expires, &stop); err != nil {
			return n, fmt.Errorf("%w: scan: %v", ErrStoreUnavailable, err)
		}
		ttl := time.Until(expires)
		if abs := time.Until(stop); abs < ttl {
			ttl = abs // never warm a row past its hard lifetime cap
		}
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

// LinkUser associates a live session with an authenticated user id (called on
// login, after rotation). It mirrors the Redis-side switch of rate-limit keys
// from session-scoped to user-scoped and is what makes ActiveUserCount honest.
func (f *SessionFallback) LinkUser(ctx context.Context, token, userID string) error {
	_, err := f.pool.Exec(ctx, sessionSetUserSQL, token, userID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}
	return nil
}

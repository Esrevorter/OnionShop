package server

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis-backed sliding-window rate limiting keyed by identity — NEVER by
// client IP: every request arrives from 127.0.0.1 through Tor, so an IP key
// would collapse the whole onion into one bucket (SECURITY_AND_OPSEC.md §1
// "No IP Rate Limiting").
//
// Key space: rl:<bucket>:<hash(identity)>. Raw user ids and session tokens are
// hashed before they become key material so a Redis dump or debug log can
// never hand out live session identifiers.
//
// Anonymous visitors mint fresh buckets for free by creating new sessions, so
// per SECURITY_AND_OPSEC.md §1 pre-auth endpoints must ALSO run CAPTCHA +
// honeypot friction; the shared global bucket below is the throughput backstop
// that keeps session-minting uneconomical.
const (
	// HeaderRetryAfter advertises seconds until the window frees a slot.
	HeaderRetryAfter = "Retry-After"
	// GlobalBucketName throttles total anonymous traffic regardless of session
	// count (the anti-session-minting cap).
	GlobalBucketName = "global"
)

// ErrRedisUnavailable marks a dead limiter backend. State-changing requests
// fail closed against it (see RateLimit); safe reads fail open.
var ErrRedisUnavailable = errors.New("server: rate limiter unavailable")

// Bucket configures one limit.
type Bucket struct {
	Name   string        // stable identifier used in the Redis key
	Limit  int           // max events per Window
	Window time.Duration // sliding window length
}

// DefaultBuckets returns the Phase 2 starting point. Keep names stable — they
// are part of the Redis key namespace.
func DefaultBuckets() map[string]Bucket {
	return map[string]Bucket{
		"default":        {Name: "default", Limit: 300, Window: time.Minute},
		"login":          {Name: "login", Limit: 10, Window: 5 * time.Minute},
		"register":       {Name: "register", Limit: 3, Window: time.Hour},
		"captcha":        {Name: "captcha", Limit: 20, Window: 5 * time.Minute},
		"ticket":         {Name: "ticket", Limit: 5, Window: time.Hour},
		"message":        {Name: "message", Limit: 30, Window: 5 * time.Minute},
		GlobalBucketName: {Name: GlobalBucketName, Limit: 80, Window: time.Minute},
	}
}

// RateLimiter implements a fixed-count sliding window with one sorted set per
// (bucket, identity): scores are event timestamps, members are unique per
// event, so concurrent duplicates cannot share a slot. Prune + count + add run
// in a single Lua script to stay atomic under concurrency.
type RateLimiter struct {
	rdb    *redis.Client
	bucket Bucket
}

// NewRateLimiter binds a limiter to one bucket over an existing client. The
// caller owns the client's lifecycle (store.OpenRedis validates it at boot).
func NewRateLimiter(rdb *redis.Client, bucket Bucket) *RateLimiter {
	return &RateLimiter{rdb: rdb, bucket: bucket}
}

// windowScript atomically prunes old events, counts the window, and records
// the current event when there is room.
// KEYS[1]=active set; ARGV: now(ms), window(ms), limit, member.
// Returns {allowed(0/1), retry_after_seconds}.
var windowScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local win = tonumber(ARGV[2])
local lim = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - win)
local n = redis.call('ZCARD', KEYS[1])
if n < lim then
  redis.call('ZADD', KEYS[1], now, ARGV[4])
  redis.call('PEXPIRE', KEYS[1], win)
  return {1, 0}
end
local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local retry = 0
if #oldest == 2 then
  retry = math.max(1, math.ceil((tonumber(oldest[2]) + win - now) / 1000))
end
return {0, retry}
`)

// Decision reports the outcome of one Allow call.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Allow records one event for identity if the window has room. identity MUST be
// a user id or session token — never an IP address (AGENTS.md §1).
func (l *RateLimiter) Allow(ctx context.Context, identity string) (Decision, error) {
	if l.rdb == nil {
		return Decision{}, ErrRedisUnavailable
	}
	if identity == "" {
		identity = "anon"
	}
	now := time.Now()
	key := fmt.Sprintf("rl:%s:%s", l.bucket.Name, hashKey(identity))
	member := fmt.Sprintf("%d-%d", now.UnixNano(), randSeq())

	res, err := windowScript.Run(ctx, l.rdb, []string{key},
		now.UnixMilli(),
		l.bucket.Window.Milliseconds(),
		l.bucket.Limit,
		member,
	).Result()
	if err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrRedisUnavailable, err)
	}
	arr, ok := res.([]int64)
	if !ok {
		// go-redis may surface Lua tables as []interface{} depending on
		// version; normalise defensively instead of panicking on a hot path.
		if v, isIface := res.([]interface{}); isIface && len(v) == 2 {
			a, _ := v[0].(int64)
			b, _ := v[1].(int64)
			arr = []int64{a, b}
		} else {
			return Decision{}, errors.New("server: unexpected lua reply from rate-limit script")
		}
	}
	d := Decision{Allowed: arr[0] == 1}
	if !d.Allowed && arr[1] > 0 {
		d.RetryAfter = time.Duration(arr[1]) * time.Second
	}
	return d, nil
}

// Remaining is a cheap read-only peek for dashboards (consumes nothing).
func (l *RateLimiter) Remaining(ctx context.Context, identity string) (int, error) {
	if l.rdb == nil {
		return 0, ErrRedisUnavailable
	}
	if identity == "" {
		identity = "anon"
	}
	key := fmt.Sprintf("rl:%s:%s", l.bucket.Name, hashKey(identity))
	n, err := l.rdb.ZCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrRedisUnavailable, err)
	}
	left := l.bucket.Limit - int(n)
	if left < 0 {
		left = 0
	}
	return left, nil
}

// LimiterConfig wires the middleware to the session layer.
type LimiterConfig struct {
	// RDB is the shared Redis client (same one the session store uses).
	RDB *redis.Client
	// BucketFor maps (request, userID) to a bucket name; "" means unlimited.
	BucketFor func(r *http.Request, userID string) string
	// Buckets is the name -> Bucket table (nil selects DefaultBuckets()).
	Buckets map[string]Bucket
	// UserID extracts the authenticated identity ("" when anonymous).
	UserID func(r *http.Request) string
	// SessionID extracts the anonymous fallback identity (session token).
	SessionID func(r *http.Request) string
	// OnDenied renders the rejection; default emits 429 + Retry-After only.
	OnDenied func(w http.ResponseWriter, r *http.Request, d Decision)
}

// RateLimit middleware. Mount AFTER sessions (so UserID/SessionID resolve)
// and BEFORE CSRF/body parsing (a flood should not burn CPU on form parsing).
//
// Identity precedence: authenticated user_id wins; anonymous traffic rides its
// session token AND the shared global bucket, so minting N sessions still
// costs N slots of the same global window.
//
// Failure policy — deliberate asymmetry, do not "simplify": if Redis is down
// we fail CLOSED for state-changing methods (503 + Retry-After) because an
// unthrottled write path is the actual abuse surface, and fail OPEN for safe
// GETs because blackholing the storefront during a cache blip is worse than a
// brief unthrottled read burst.
func RateLimit(cfg LimiterConfig) func(http.Handler) http.Handler {
	if cfg.Buckets == nil {
		cfg.Buckets = DefaultBuckets()
	}
	if cfg.OnDenied == nil {
		cfg.OnDenied = write429
	}
	if cfg.BucketFor == nil {
		cfg.BucketFor = func(*http.Request, string) string { return "default" }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := ""
			if cfg.UserID != nil {
				userID = cfg.UserID(r)
			}
			name := cfg.BucketFor(r, userID)
			if name == "" {
				next.ServeHTTP(w, r)
				return
			}
			bucket, ok := cfg.Buckets[name]
			if !ok || cfg.RDB == nil {
				// Misconfiguration or missing backend: apply the failure policy.
				if isSafeMethod(r.Method) {
					next.ServeHTTP(w, r)
					return
				}
				w.Header().Set(HeaderRetryAfter, "5")
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			identity := userID
			if identity == "" && cfg.SessionID != nil {
				identity = cfg.SessionID(r)
			}
			lim := NewRateLimiter(cfg.RDB, bucket)
			d, err := lim.Allow(r.Context(), identity)
			if err != nil {
				if isSafeMethod(r.Method) {
					next.ServeHTTP(w, r) // fail open for reads
					return
				}
				w.Header().Set(HeaderRetryAfter, "5")
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			if !d.Allowed {
				cfg.OnDenied(w, r, d)
				return
			}
			// Global throughput cap for anonymous traffic (anti-minting).
			if userID == "" {
				if gb, ok := cfg.Buckets[GlobalBucketName]; ok {
					gd, gerr := NewRateLimiter(cfg.RDB, gb).Allow(r.Context(), GlobalBucketName)
					if gerr == nil && !gd.Allowed {
						cfg.OnDenied(w, r, gd)
						return
					}
					// gerr != nil here just means the cap could not be counted;
					// the per-identity decision already succeeded, so proceed.
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// write429 is the minimal denial response: status + Retry-After, no explanation
// body (uniform UX; templated error pages replace this in Phase 3).
func write429(w http.ResponseWriter, _ *http.Request, d Decision) {
	secs := int(d.RetryAfter / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set(HeaderRetryAfter, strconv.Itoa(secs))
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

// hashKey folds an identity into a short stable digest so raw tokens/user ids
// never appear verbatim in Redis keyspace dumps (SECURITY_AND_OPSEC.md §2 data
// minimisation applied to infrastructure metadata).
func hashKey(s string) string {
	const offset, prime = uint64(5381), uint64(33)
	h := offset
	for i := 0; i < len(s); i++ {
		h = h*prime ^ uint64(s[i])
	}
	return strconv.FormatUint(h, 16)
}

// randSeq adds entropy to the ZSET member so two requests landing in the same
// nanosecond cannot overwrite each other's slot. A CSPRNG hiccup degrades
// dedupe precision only (never secrecy), so fall back to time rather than
// panic on a request hot path.
func randSeq() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint64(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint64(b[:])
}

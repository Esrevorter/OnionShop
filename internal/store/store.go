// Package store owns every data-layer connection: the Postgres pool (pgx),
// the Redis client (ephemeral state: rate limits, carts, CAPTCHA tokens) and
// the durable sessions-fallback store used by the scs session manager when
// Redis is unavailable or evicts (ARCHITECTURE.md §2).
//
// Rules that must not regress (AGENTS.md §2, CONTRIBUTING.md §6):
//   - pgx only — never an ORM;
//   - every query takes a context.Context;
//   - no client IPs or other PII ever reach this layer from logs.
package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Env variable names read by FromEnv. Compose sets DATABASE_URL / REDIS_URL
// (see docker-compose.yml); ONIONSHOP_DB_* are the granular fallbacks.
const (
	EnvDatabaseURL = "DATABASE_URL"
	EnvRedisURL    = "REDIS_URL"
	EnvDBHost      = "ONIONSHOP_DB_HOST"
	EnvDBPort      = "ONIONSHOP_DB_PORT"
	EnvDBName      = "ONIONSHOP_DB_NAME"
	EnvDBUser      = "ONIONSHOP_DB_USER"
	EnvDBPassword  = "ONIONSHOP_DB_PASSWORD"
	EnvDBSSLMode   = "ONIONSHOP_DB_SSLMODE"
)

// DBConfig describes the Postgres connection pool. Zero fields fall back to
// the defaults in DefaultDBConfig.
type DBConfig struct {
	URL string // full DSN/envuri; wins over the granular fields below

	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string

	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
}

// DefaultDBConfig returns conservative pool settings for a single-app-instance
// deployment on a hardened host (Postgres local max_connections is small and
// there is no reason to inflate it).
func DefaultDBConfig() DBConfig {
	return DBConfig{
		Host:            "127.0.0.1",
		Port:            5432,
		Name:            "onionshop",
		User:            "onionshop",
		SSLMode:         "disable", // Postgres rides the compose internal network / local socket
		MaxConns:        8,
		MinConns:        1,
		MaxConnLifetime: time.Hour,
		ConnectTimeout:  10 * time.Second,
	}
}

// dsn renders the effective connection string (URL if set, else granular fields).
func (c DBConfig) dsn() (string, error) {
	if strings.TrimSpace(c.URL) != "" {
		return c.URL, nil
	}
	if c.Host == "" || c.Name == "" || c.User == "" {
		return "", errors.New("store: no Postgres configuration (set DATABASE_URL or ONIONSHOP_DB_HOST/NAME/USER)")
	}
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:     c.Name,
		RawQuery: "sslmode=" + ssl,
	}
	return u.String(), nil
}

// OpenDB creates the pgx connection pool and verifies connectivity with a
// bounded ping so a misconfigured deployment fails fast at startup instead of
// serving anonymous half-broken sessions later.
func OpenDB(ctx context.Context, cfg DBConfig) (*pgxpool.Pool, error) {
	dsn, err := cfg.dsn()
	if err != nil {
		return nil, err
	}
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse postgres config: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	pcx := pcfg.Copy()
	pcx.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, pcx)
	if err != nil {
		return nil, fmt.Errorf("store: create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: postgres unreachable: %w", err)
	}
	return pool, nil
}

// OpenDBFromEnv opens the pool using environment-derived configuration.
func OpenDBFromEnv(ctx context.Context) (*pgxpool.Pool, error) {
	cfg := DefaultDBConfig()
	cfg.URL = os.Getenv(EnvDatabaseURL)
	if v := os.Getenv(EnvDBHost); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv(EnvDBPort); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("store: invalid %s: %w", EnvDBPort, err)
		}
		cfg.Port = port
	}
	if v := os.Getenv(EnvDBName); v != "" {
		cfg.Name = v
	}
	if v := os.Getenv(EnvDBUser); v != "" {
		cfg.User = v
	}
	if v := os.Getenv(EnvDBPassword); v != "" {
		cfg.Password = v
	}
	if v := os.Getenv(EnvDBSSLMode); v != "" {
		cfg.SSLMode = v
	}
	return OpenDB(ctx, cfg)
}

// RedisConfig describes the ephemeral-state Redis connection.
type RedisConfig struct {
	URL          string // redis://[:password@]host:port/db
	Network      string // "tcp" (default) or "unix"
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
	// SessionDB is the logical database holding session keys; compose runs
	// Redis with maxmemory-policy noeviction so sessions are never silently
	// dropped under memory pressure (ARCHITECTURE.md §2).
	SessionDB int
}

// DefaultRedisConfig returns loopback TCP defaults matching docker-compose.
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Network:      "tcp",
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     16,
	}
}

// OpenRedis parses cfg.URL (or $REDIS_URL when cfg.URL is empty) and pings the
// server so operators learn about a dead cache at startup, not in production.
func OpenRedis(ctx context.Context, cfg RedisConfig) (*redis.Client, error) {
	url := cfg.URL
	if url == "" {
		url = os.Getenv(EnvRedisURL)
	}
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("store: no Redis configuration (set REDIS_URL)")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("store: parse REDIS_URL: %w", err)
	}
	if cfg.Network != "" {
		opts.Network = cfg.Network
	}
	if cfg.DialTimeout > 0 {
		opts.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		opts.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		opts.WriteTimeout = cfg.WriteTimeout
	}
	if cfg.PoolSize > 0 {
		opts.PoolSize = cfg.PoolSize
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, opts.DialTimeout+time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("store: redis unreachable: %w", err)
	}
	return client, nil
}

// OpenRedisFromEnv connects using $REDIS_URL (empty URL is an error: the app
// must not silently run without its ephemeral-state backend).
func OpenRedisFromEnv(ctx context.Context) (*redis.Client, error) {
	return OpenRedis(ctx, DefaultRedisConfig())
}

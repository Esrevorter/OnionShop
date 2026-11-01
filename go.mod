module github.com/example/onionshop

// The codebase uses generics and modern stdlib APIs; go 1.19 was stale and
// caused CI type-check failures resolving third-party packages.
// Bumped to 1.23: golangci-lint v1.64.8 is built with Go ≥1.23, and a lower
// language version in go.mod made its module-graph resolution diverge from
// the passing vet/test steps on CI (stale typecheck cascades). setup-go reads
// this file via go-version-file, so runners now fetch Go 1.23.x automatically.
go 1.23

require (
	github.com/alexedwards/scs/v2 v2.8.0
	github.com/go-chi/chi/v5 v5.0.12
	github.com/jackc/pgx/v5 v5.5.5
	github.com/redis/go-redis/v9 v9.6.1
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle/v2 v2.2.1 // indirect
	golang.org/x/crypto v0.21.0 // indirect
	golang.org/x/sync v0.5.0 // indirect
	golang.org/x/text v0.14.0 // indirect
)

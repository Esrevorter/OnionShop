module github.com/example/onionshop

// Language/toolchain floor. History: 1.19 broke golangci-lint module-graph
// resolution; 1.23 aligned it with the pinned linter's build toolchain. It now
// sits at 1.25.0 because pgx v5.9.x requires go >= 1.25.0 — `go mod tidy`
// enforces that floor transitively. CI pins the runner to an exact matching
// toolchain (see .github/workflows/ci.yml): golangci-lint v1.64.8 chokes on
// export data produced by Go >= 1.25/1.27-era compilers when the pin drifts,
// so the runner version must track this directive deliberately, never "latest".
go 1.25.0

require (
	github.com/alexedwards/scs/v2 v2.8.0
	github.com/go-chi/chi/v5 v5.0.12
	github.com/jackc/pgx/v5 v5.9.2
	github.com/redis/go-redis/v9 v9.6.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

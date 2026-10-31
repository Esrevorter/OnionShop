# OnionShop

A custom multi-vendor shopping website designed for deployment as a Tor v3 hidden service.
No JavaScript, no CDNs, no IP logging — server-rendered Go HTML over a localhost-only socket.

> **Status:** early foundation phase. See [TASKS.md](TASKS.md) for the roadmap; most of it is
> not yet implemented.

## Documentation
| File | Contents |
|---|---|
| [AGENTS.md](AGENTS.md) | Hard rules for AI agents & contributors (no JS, template/CSRF syntax, cookie flags, Argon2id params) |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Request flow, components, BTCPay polling (no webhooks), security headers, escrow state machine, ledger design |
| [SECURITY_AND_OPSEC.md](SECURITY_AND_OPSEC.md) | Tor-specific OpSec, data minimization, payment/escrow integrity, audit trail |
| [TASKS.md](TASKS.md) | Phased roadmap with current status |

## Tech Stack
- **Go** (`chi` router, `html/template`, `scs` sessions, `pgx`/`sqlc`, `argon2id`)
- **PostgreSQL** — users, products, orders, double-entry ledger, audit log
- **Redis** — rate limits, carts, session store (with Postgres fallback)
- **BTCPay Server** — BTC invoices, polled by the backend (hidden services cannot receive webhooks)
- **Tor daemon** — hidden service forwarding to `127.0.0.1:8080`

## Repository Layout
```
cmd/onionshop/       entrypoint (HTTP server + background poller/cron)
internal/server/     chi router, middleware (security headers, CSRF, sessions, rate limit)
internal/store/      pgx/sqlc data access, ledger & escrow services
web/templates/       html/template pages (no JS anywhere)
web/static/          locally-served CSS/images/fonts only
db/migrations/       SQL migrations (golang-migrate/goose)
docker-compose.yml   Postgres + Redis (+ optional BTCPay) for development
```

## Development
```bash
make up        # start Postgres + Redis via docker compose
make migrate   # apply db/migrations
make run       # go run ./cmd/onionshop  (binds 127.0.0.1:8080 only)
make test      # go test ./... plus vet/lint in CI
```
Production deployments additionally configure the Tor hidden service to point at
`127.0.0.1:8080`; the app itself never binds a public interface.

## License
MIT — see [LICENSE](LICENSE).

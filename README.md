# OnionShop

A custom multi-vendor shopping website designed for deployment as a Tor v3 hidden service.
No JavaScript, no CDNs, no IP logging — server-rendered Go HTML behind an nginx reverse proxy
(loopback-only), HTTPS on the onion via a self-signed SPKI-pinned cert, encrypted at rest (LUKS2),
admin access over Dropbear-through-a-private-onion.

> **Status:** early foundation phase. See [TASKS.md](TASKS.md) for the roadmap; most of it is
> not yet implemented.

## Documentation
| File | Contents |
|---|---|
| [AGENTS.md](AGENTS.md) | Hard rules for AI agents & contributors (no JS, template/CSRF syntax, cookie flags, Argon2id params, CAPTCHA rules) |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Request flow (Tor → nginx → Go over UNIX socket), components, BTCPay polling (no webhooks), security headers, escrow state machine, ledger, support ticketing, messaging, profiles/community features |
| [SECURITY_AND_OPSEC.md](SECURITY_AND_OPSEC.md) | Threat model, Tor/onion hardening, nginx + firewall + tmpfs + Dropbear infrastructure controls, encryption at rest, data minimization, payment/escrow integrity, audit trail, anti-phishing measures, incident response |
| [TASKS.md](TASKS.md) | Phased roadmap with current status |

## Tech Stack
- **Go** (`chi` router, `html/template`, `scs` sessions, `pgx`/`sqlc`, `argon2id`)
- **nginx** — loopback-only reverse proxy: TLS termination (self-signed .onion cert, TLS 1.3/AEAD),
  static files, header stripping, UNIX-socket upstream to the app
- **PostgreSQL** — users, products, orders, double-entry ledger, audit log (data on LUKS volume)
- **Redis** — rate limits, carts, session store (with Postgres fallback); RAM-only (persistence off)
- **BTCPay Server** — BTC invoices, polled by the backend (hidden services cannot receive webhooks)
- **Tor daemon** — hidden service forwarding to `127.0.0.1:8080`/`8443` (nginx); admin SSH rides a
  separate private onion to Dropbear

## Repository Layout
```
cmd/onionshop/       entrypoint (HTTP server + background poller/cron)
internal/server/     chi router, middleware (security headers, CSRF, sessions, rate limit)
internal/store/      pgx/sqlc data access, ledger & escrow services
web/templates/       html/template pages (no JS anywhere)
web/static/          locally-served CSS/images/fonts only (mounted read-only into nginx)
db/migrations/       SQL migrations (golang-migrate/goose)
docker-compose.yml   Postgres + Redis + nginx (+ optional BTCPay) for development
```

## Development
```bash
make up        # start Postgres + Redis via docker compose
make migrate   # apply db/migrations
make run       # go run ./cmd/onionshop  (binds 127.0.0.1:8080 / UNIX socket only)
make test      # go test ./... plus vet/lint in CI
```
Production deployments additionally configure the Tor hidden service to point at nginx on
`127.0.0.1:8080`/`8443` (TLS with a self-signed, SPKI-pinned .onion cert); nginx proxies to the
app over a UNIX socket; neither component binds a public interface, and remote admin uses
Dropbear over a private onion — never OpenSSH on clearnet (SECURITY_AND_OPSEC.md §5).

## License
MIT — see [LICENSE](LICENSE).

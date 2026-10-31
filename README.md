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
| [INSTALL.md](INSTALL.md) | Operator guide: prerequisites, quick start (`make install`), post-install steps, backup/panic-wipe runbooks |
| [TASKS.md](TASKS.md) | Phased roadmap with current status |

## Tech Stack
- **Go** (`chi` router, `html/template`, `scs` sessions, `pgx`/`sqlc`, `argon2id`)
- **nginx** — loopback-only reverse proxy: TLS termination (self-signed .onion cert, TLS 1.3/AEAD),
  static files, header stripping, UNIX-socket upstream to the app
- **PostgreSQL** — users, products, orders, double-entry ledger, audit log (data on LUKS volume)
- **Redis** — rate limits, carts, session store (with Postgres fallback); RAM-only (persistence off)
- **BTCPay Server** — BTC invoices, polled by the backend (hidden services cannot receive webhooks)
- **Tor daemon** — hidden service forwarding to nginx on `127.0.0.1:8443` (TLS); admin SSH rides a
  separate private onion to Dropbear

## Repository Layout
```
cmd/onionshop/       entrypoint (HTTP server + background poller/cron)
internal/server/     chi router, middleware (security headers, CSRF, sessions, rate limit)
internal/store/      pgx/sqlc data access, ledger & escrow services
web/templates/       html/template pages (no JS anywhere)
web/static/          locally-served CSS/images/fonts only (mounted read-only into nginx)
db/migrations/       SQL migrations (golang-migrate/goose)
docker-compose.yml   hardened stack: Postgres + Redis + nginx + app (+ optional BTCPay)
Dockerfile           multi-stage static build, non-root, read-only-rootfs compatible
nginx/               nginx.conf (TLS 1.3, UNIX-socket upstream) + ssl/ (gitignored certs)
scripts/             gen-certs.sh, backup.sh (age-encrypted dumps), panic-wipe.sh
deploy/              torrc + nftables.conf host templates
systemd/             sandboxed service units for the bare-metal path
install/install.sh   guided one-shot deployment (see INSTALL.md)
.env.example         documented secret template (.env itself is gitignored — never commit it)
```

## Installation
For less-experienced operators, the guided installer automates everything that is safe to
automate (secrets, certs, compose bring-up) and refuses to touch destructive host hardening:
```bash
cp .env.example .env && vi .env      # fill in real values; chmod 600 enforced by installer
make install                         # == sudo ./install/install.sh  (full guide: INSTALL.md)
```
Host prerequisites (LUKS2, nftables default-deny, Tor, Dropbear admin onion) are **manual gates**
the installer will make you acknowledge — see [INSTALL.md](INSTALL.md) and SECURITY_AND_OPSEC.md §5.

## Development
```bash
make up        # start full stack via docker compose (requires .env)
make migrate   # apply db/migrations
make run       # go run ./cmd/onionshop  (binds 127.0.0.1:8080 / UNIX socket only)
make test      # go test ./... plus vet/lint in CI
make certs     # generate self-signed onion cert + print SPKI pin (scripts/gen-certs.sh)
```
Production deployments additionally configure the Tor hidden service to point at nginx on
`127.0.0.1:8443` (TLS with a self-signed, SPKI-pinned .onion cert); nginx proxies to the
app over a UNIX socket (`/run/onionshop/app.sock`); neither component binds a public interface,
and remote admin uses Dropbear over a private onion — never OpenSSH on clearnet
(SECURITY_AND_OPSEC.md §5).

## License
MIT — see [LICENSE](LICENSE).

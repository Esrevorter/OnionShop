# Contributing to OnionShop

## Ground rules
1. **Server-rendered HTML only.** CSP is `script-src 'none'` — no JavaScript, no
   external assets, no CDN. Interactive controls are native forms and `<details>`.
2. **Tor Browser is the supported client.** TLS is a self-signed ECDSA P-384 leaf
   whose DNS SAN is exactly the v3 `.onion` hostname. **No CA-issued certificates,
   no SPKI/certificate pins, no HSTS** — see `docs/DECISIONS.md` ADR-001.
3. **Never log or use client IP addresses.** Rate-limit by authenticated identity;
   use CAPTCHA/honeypot friction. No email, no third-party requests.
4. **Settlement is our own `monero-wallet-rpc` client** against our own `monerod`.
   No BTCPay, no MoneroPay (`docs/DECISIONS.md` ADR-002). Payment status is obtained
   by **polling**; never add an inbound webhook or callback route.
5. **Money is integer cents/piconero.** Parse decimal input with `parseDecimalUnits`
   (or `parseDecimalUnitsAllowZero` where zero is meaningful). Never `ParseFloat`
   in a money path; whole-ledger aggregates use NUMERIC.
6. **Only public PGP keys are stored.** The server never decrypts anything.
7. **Escrow transitions route through `escrow.go` `Apply`**, with a status guard in
   the SQL `UPDATE`. Ledger legs are double-entry, append-only and idempotent.

## Friction contract
A CAPTCHA may only be required where the GET that renders the form **mints a
challenge**. Low-risk POSTs (cart actions, publish/remove, shipping toggles,
message replies) use CSRF + honeypot only. An un-mintable challenge silently
swallows every submission — this bug class has been fixed twice; do not reintroduce it.

## Local gates
```sh
export GOTMPDIR="$HOME/.gotmp"   # required where /tmp is noexec (Whonix)
sqlc generate                    # internal/store/gen is gitignored
go build ./...
go vet ./internal/server
go test ./internal/server/... -count=1
bash -n install/install.sh scripts/*.sh
docker compose config --quiet
docker compose build
```

## Running the stack
```sh
sudo ./install/install.sh --dev          # Debian 13; bootstraps, migrates, starts
./install/install.sh --check-only        # read-only preflight, no root needed
```
The installer applies `db/migrations/*.up.sql` idempotently. `make migrate` needs
`DATABASE_URL` from your environment because the installer generates a random
DB password.

Alternatively `python3 deploy.py` automates pull → build/test → postgres up →
migrations (same psql + `schema_migrations` ledger as install.sh) → restart →
`/healthz` verification. It needs no local psql (runs SQL via
`docker compose exec`). The app exposes exactly one health route, `/healthz`;
do not write checks or docs against `/health`.

## Migrations
Add `NNNN_name.up.sql` **and** `NNNN_name.down.sql`. Never edit an applied
migration. Money columns are integer minor units; no float conversions, ever.

## Commits
Keep changes scoped and explain the *why*. Documentation that contradicts a
settled ADR is a defect — update it in the same commit as the code.

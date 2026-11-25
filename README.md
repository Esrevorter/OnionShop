# OnionShop

A Tor-only, server-rendered marketplace with **Monero escrow settlement**.
No JavaScript, no email, no third-party assets, no client-IP logging.

> **Status: development preview.** The full commerce loop runs end to end on a
> private Monero testnet. A green stack is **not** production clearance — see
> [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) before handling real value.

## What works today

**Buyers** — register (CAPTCHA + honeypot), optional TOTP 2FA, anti-phishing phrase,
browse/search in USD/EUR/AUD, per-vendor cart and shipping selection, checkout with
PGP-encrypted delivery details and a required refund address, exact-amount XMR payment
to a per-order subaddress, escrow, confirm receipt or dispute, one-time escrow extension,
pre-shipment cancel with on-chain refund, reviews, in-app messages, support tickets.

**Vendors** — apply, pay the operator-set registration fee in XMR, await approval,
publish a PGP public key (required to receive orders), define shipping options,
manage listings and stock, decrypt delivery details offline, mark shipped,
ledger-backed wallet with available/pending balances, withdrawals paid on-chain.

**Operators** — dashboard, vendor queue, fee setting, user/session administration,
dispute resolution (release or refund), withdrawal review, support tickets,
append-only audit log, automated escrow timers.

## Architecture

Tor Browser → Tor → **Whonix-Gateway** (v3 onion service) → **Whonix-Workstation**
(nginx, TLS 1.3, UNIX socket) → Go app → PostgreSQL / Redis / `monerod` /
`monero-wallet-rpc`. The Workstation has no physical NIC; all traffic routes through
the Gateway. Wallet RPC is internal-only with **no published ports**.

Settlement talks JSON-RPC directly to `monero-wallet-rpc` (`create_address` per order,
`get_transfers` for observation). **No MoneroPay, no BTCPay.** Detection is **polling
only** — never inbound webhooks, because the hidden service is unreachable inbound.

## Money model

- Prices are integer **USD cents**; settlement is integer **piconero** (1 XMR = 1e12).
- Decimal input is parsed exactly (`parseDecimalUnits`), never via floating point.
- Invoices are rounded to 6 decimals **at creation**, so the displayed amount is
  exactly the payable amount: `0.733333 XMR`.
- A transfer confirms an order only on **its own subaddress**, at the **exact amount**,
  after the confirmation threshold. Overpayment is flagged for manual review and never
  auto-releases; underpayment records the delta and asks for the remainder.
- Ledger movements are double-entry, append-only and idempotent
  (`release:<order>`, `refund:<order>`, `withdraw:<id>`).

## Escrow lifecycle

| State | Meaning | Next |
|---|---|---|
| `awaiting_payment` | invoice issued | pay within 12 h, else cancelled |
| `in_escrow` | payment confirmed | vendor ships, or buyer cancels for refund |
| `shipped` | vendor dispatched | buyer releases, extends once, or disputes |
| `disputed` | moderator reviewing | resolve to release or refund |
| `released` | completed | vendor balance credited; review window open |
| `refunded` | value returned | sent on-chain to the buyer's refund address |
| `cancelled` | unpaid/expired | nothing charged |

Hourly cron: 12 h unpaid → cancel; 3 days in escrow without shipment → refund;
shipped + shipping estimate + 3-day grace → auto-release (blocked by an open dispute).
Every event posts an audience-targeted message to the buyer/vendor thread.

## Quick start (Debian 13 minimal)

```sh
git clone <your-repo> OnionShop && cd OnionShop
sudo ./install/install.sh --dev                     # bootstrap, secrets, dev TLS, migrations, stack
sudo ./install/install.sh --onion <host>.onion      # real onion SAN certificate
sudo ./install/install.sh --whonix --onion <host>.onion
./install/install.sh --check-only                   # read-only preflight, no root needed
```

Idempotent: keeps existing `.env` values, applies pending `db/migrations/*.up.sql`
in order (tracked in `schema_migrations`), waits for the nginx→app health path.
Help Center: `/help`, `/help/buyers`, `/help/vendors`, `/help/admins`.

## Development

```sh
export GOTMPDIR="$HOME/.gotmp"   # required where /tmp is noexec (Whonix)
sqlc generate                    # generated store code is gitignored
go build ./... && go vet ./internal/server && go test ./internal/server/... -count=1
bash -n install/install.sh scripts/*.sh
docker compose config --quiet && docker compose build
```

Private dev chain: `monerod --testnet --offline --fixed-difficulty 1` with a matching
`--testnet` wallet-rpc. Mine via the legacy `/start_mining` endpoint (`generateblocks`
is regtest-only, and wallet-rpc has no regtest mode).

## Security policy (non-negotiable)

- **Tor Browser only.** Self-signed ECDSA P-384 leaf with the exact `.onion` DNS SAN.
  **No CA-issued certificates, no SPKI/certificate pinning, no HSTS.** Never advise
  bypassing a browser warning.
- No JavaScript (`script-src 'none'`), no client-IP rate limiting or logging, no email.
- Only **public** PGP keys are stored; the server never decrypts anything.
- Mainnet requires an airgapped **view-only** wallet ceremony; spend keys never touch
  the server. The dev testnet wallet is not a custody model.
- Backups are age-encrypted to an offline recipient; restore drills are mandatory.

## Documentation

| File | Purpose |
|---|---|
| [INSTALL.md](INSTALL.md) | Debian 13 quick start and Whonix topology |
| [ARCHITECTURE.md](ARCHITECTURE.md) | request flow, components, escrow timers |
| [SECURITY_AND_OPSEC.md](SECURITY_AND_OPSEC.md) | threat model, TLS/network policy, Monero posture |
| [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) | deployment gates and evidence required |
| [UI.md](UI.md) | shipped UI state and UX decisions |
| [TASKS.md](TASKS.md) | open engineering gates and settled decisions |
| [AUDIT.md](AUDIT.md) | findings ledger with closure evidence |
| [AGENTS.md](AGENTS.md) | contributor/agent working contract |
| [CONTRIBUTING.md](CONTRIBUTING.md) | development workflow and gates |
| [docs/archive/](docs/archive/) | superseded plans kept for rationale |

## License

MIT — see [LICENSE](LICENSE).

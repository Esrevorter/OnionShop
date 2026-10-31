# System Architecture

## 1. Request Flow
1. User (Tor Browser) -> Tor Network -> Tor Daemon (v3 Hidden Service).
2. Tor Daemon -> forwards to `127.0.0.1:8080` (Go Web Server).
3. Go Web Server -> renders HTML via `html/template` -> sends back through Tor.

## 2. Component Interaction
- **Go Backend:** Handles routing (`chi`), business logic, and rendering.
- **PostgreSQL:** Stores persistent data (Users, Products, Orders, Ledger).
- **Redis:** Stores ephemeral data (Rate Limit counters, Shopping Carts). Sessions are stored
  via `scs` in Redis with a Postgres fallback table so an eviction/restart cannot log everyone
  out or, worse, resurrect stale carts. Redis must run with `maxmemory-policy noeviction` for
  session keys (carts/rate-limits may use `volatile-lru` in a separate logical DB if needed).
- **BTCPay Server:** Runs as a separate service on the same host.

### BTCPay payment notifications (no webhooks!)
**Webhooks are impossible here.** A hidden service is reachable only via the Tor network;
BTCPay (running on clearnet or a different hidden service) cannot POST to our localhost-only
server, and exposing a public callback URL would deanonymize the deployment. Instead:
- The Go backend **polls** BTCPay's REST API (`GET /api/v1/invoices/{id}`) on a short interval
  (e.g., every 15s for pending invoices, backed off for older ones) from a background goroutine.
- Invoice creation is idempotent: we store BTCPay's `invoiceId` and a unique `outOrderRef`
  per order; poll results are applied through the escrow state machine with an idempotency key
  so duplicate/late updates can never double-credit.
- If a future build colocates BTCPay behind the *same* hidden service under `/notify/...`,
  signed webhooks (`BTCPay-Sig` HMAC) may be added as an optimization — polling remains the
  source of truth regardless.

## 3. Security Headers
Applied by middleware on every response. **No HSTS**: it is meaningless (and dangerous) without
TLS, and this deployment terminates at plain HTTP inside the Tor circuit.
- `Content-Security-Policy: default-src 'self'; script-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`
  (`style-src 'unsafe-inline'` is required because templates ship small inline styles; if we
  move all CSS to static files, tighten to `style-src 'self'`.)
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Referrer-Policy: no-referrer`
- Strip the `Server:` header (or set a generic value); never register `pprof`/`expvar`.

## 4. Escrow State Machine
- `awaiting_payment` -> (payment confirmed via BTCPay poll) -> `in_escrow`
- `in_escrow` -> (Vendor marks shipped) -> `shipped`
- `shipped` -> (Customer confirms OR 14-day cron) -> `released` (Credits Vendor Ledger)
- `shipped` -> (Dispute opened) -> `disputed` -> (Mod resolves) -> `released` OR `refunded`

Failure / edge paths:
- `awaiting_payment` -> (expiry timer, e.g. 30 min unpaid) -> `cancelled` (cart restored).
- `awaiting_payment` -> (underpayment) -> stays `awaiting_payment` with delta invoice option;
  (overpayment) -> flag for manual review, never auto-release.
- `in_escrow` -> (customer cancels before shipment) -> `refunded`.
- Missed/lost poll updates are safe: state transitions are driven by comparing current DB state
  against BTCPay's authoritative status, not by event delivery.
- Auto-release cron uses UTC timestamps stored in Postgres (`now()`), never client clocks, and
  writes an audit-log row for every automated transition.

## 5. Multi-Vendor Orders
One customer checkout containing items from N vendors creates **N vendor-scoped orders**
sharing one payment intent; BTCPay tracks settlement per split. Shipping addresses are
encrypted per-order with the vendor's PGP public key (see SECURITY_AND_OPSEC.md). Each order
runs its own independent escrow state machine so one bad vendor cannot freeze unrelated funds.

## 6. Ledger
Double-entry, append-only: every movement inserts immutable debit/credit rows (escrow account,
vendor earnings account, platform fee account, refund account). Balances are derived
(`SUM`) or maintained in a materialized summary updated inside the same transaction. Every
external event (payment confirmation, withdrawal payout) carries a unique idempotency key
enforced by a `UNIQUE` constraint.

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

Wallets page shows per-currency **available balance** and **in-escrow total**, plus deposit /
withdraw flows (withdrawals are requests approved by an admin — see SECURITY_AND_OPSEC.md §3).

## 7. Support Ticketing
User-facing helpdesk, reachable from the header ("Support Ticket") and a Help page.
- **Ticket model:** `tickets(id, user_id, category, order_ref NULL, subject, status,
  priority, created_at, updated_at)`; status: `open → answered → user_replied → resolved →
  closed`. Comments: `ticket_messages(id, ticket_id, author_id, body, is_staff, created_at)`.
- **Categories:** general, order/escrow problem, vendor report (e.g., phishing/scam
  accounts), payment/deposit issue, bug bounty, account recovery (lost PGP/PIN).
- **Staff queue:** mods/admins see unassigned tickets filtered by role, claim them, reply,
  resolve/closed with mandatory reason; every staff action writes the audit log.
- **Escalation:** a ticket attached to an order can be escalated into a formal dispute
  (state machine §4); resolving a dispute auto-resolves its linked ticket.
- **Notifications:** new staff replies appear as site messages (§8) and bump the unread
  badge; no email is ever sent (data minimization).
- **Anti-abuse:** rate limit ticket creation per `user_id`; new accounts get a lower daily
  cap; honeypot field on the create form.

## 8. Private Messaging
Direct user↔user messaging (buyer↔seller pre-sale, user↔staff), rendered server-side only.
- **Model:** `threads(id, participant_a, participant_b, order_ref NULL)`,
  `messages(id, thread_id, sender_id, body, read_at, created_at)`. Unread count is shown as
  a nav badge (`Messages <count>`).
- **Anti-phishing CAPTCHA (per-message):** every message form must solve a fresh, one-time
  visual challenge served from a locally-generated image set (`img-src 'self'` only — never
  external CAPTCHA services). The challenge answer is bound to the session + thread and
  expires after ~5 minutes. Rationale: phishers clone markets and impersonate vendors/buyers
  at scale; a cheap human-friction step throttles automated mass-messaging without JS.
  A hidden honeypot field supplements it. Rate limits still apply per `user_id` (§1 of
  SECURITY_AND_OPSEC.md).
- **Content rules:** plain text only (auto-escaped by `html/template`); URLs posted by other
  users are rendered as inert text, not links, to blunt phishing; users can report a message,
  which opens a `category = vendor report / abuse` support ticket carrying the message link.
- **Attachments:** none (no file upload surface). Images referenced only via market-hosted
  product images.
- **Moderation:** staff may read threads involved in disputes/tickets (audited access —
  every staff view writes the audit log).

## 9. Accounts, Profiles & Community Features
- **Vendor storefronts:** public profile pages (`/u/<handle>`) with rating, sales count, ETA
  badge, product listing link, and "message vendor" button. Vendor ratings live on completed
  orders (one review per order line, editable window, no JS — plain forms).
- **Top Vendors leaderboard:** homepage widget ranking vendors by rolling-window sales minus
  disputes/chargebacks; computed by a cron query into a snapshot table (cheap to render).
- **Account tiers:** Basic vs upgraded (verified/trusted) accounts — upgrade requires either
  vendor approval flow or a paid/trust threshold; tier gates features (e.g., higher rate
  limits, raffle entry, private mirror request). Stored as RBAC/flag on the user row.
- **Personal phrase:** each user sets a secret phrase shown on login/account pages so they
  can sanity-check they are on the genuine site (pairs with the anti-phishing CAPTCHA —
  see SECURITY_AND_OPSEC.md §4).
- **Login history:** last N logins (timestamp UTC + which mirror/host the request arrived
  on — never client IPs) displayed in account settings; anomalies prompt support contact.
- **PGP tool:** a self-service page where users paste armored text and the server performs
  pure-Go public-key encrypt/verify (key import validation, fingerprint display). No private
  keys, ever.
- **News/announcements board:** admin-authored posts (maintenance, DDoS status, feature
  notes) stored in Postgres, rendered on home; supports "under maintenance" banner mode.
- **Raffles (optional, Phase 8):** entries purchased from wallet balance; draws executed by
  cron with audited RNG (`crypto/rand`), prizes credited through the ledger like any other
  movement.
- **Help center:** static FAQ + policy pages (phishing education, harm reduction, bug bounty,
  market verify instructions, canary, published PGP key fingerprint).

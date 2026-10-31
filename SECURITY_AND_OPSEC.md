# Security & Operational Security (OpSec) Guidelines

## 1. Tor Hidden Service Specifics
- **Localhost Binding:** The Go web server MUST bind ONLY to `127.0.0.1:PORT`. Never bind to `0.0.0.0`.
- **No IP Rate Limiting:** Traditional IP-based rate limiting (like Fail2Ban) is useless here. Use Redis-based sliding window rate limiting keyed by `session_id` or `user_id`.
  - **Caveat:** anonymous attackers can mint fresh sessions to reset session-keyed limits. Wherever possible, key limits to the authenticated `user_id`; for pre-auth endpoints (login, registration, CAPTCHA), layer cheap friction first (image CAPTCHA + honeypot + global throughput cap — see AGENTS.md §3) so session-minting stays uneconomical.
- **Time Synchronization:** Do not implement custom NTP logic. Rely on the host OS. Do not leak timezones in HTTP headers.
- **Header Hygiene:** Strip/genericize the `Server:` header, never expose stack traces in error pages (render a generic template), and never register `pprof` or `expvar` routes. Serve uniform static assets (one CSS file, one font set, one image style) so per-page fingerprinting yields nothing.
- **Https:** The Go web server must be configured to serve HTTPS using a valid TLS certificate (preferably via Let's Encrypt for the .onion domain) to guarantee server authentication and defense-in-depth, even though Tor provides transport encryption.

## 2. Data Minimization & Privacy
- **No Analytics:** Do not implement Google Analytics, Plausible, or any tracking scripts.
- **Log Sanitization:** Ensure the Go logger strips out any potential PII. Log only errors and critical system events.
- **PGP Handling:** The server stores only user **public** keys and ciphertext. Private keys never touch the server; decryption of shipping addresses happens client-side on the vendor's machine. Process public-key import in memory, validate armored-block format, and never log raw key material or ciphertext.

## 3. Payment & Escrow Security
- **Payment Status via Polling (no webhooks):** A localhost-only hidden service cannot receive inbound webhooks from BTCPay (see ARCHITECTURE.md §2). The backend polls BTCPay's invoice API; every poll result is applied idempotently (unique `(invoice_id, status)` event key with a `UNIQUE` constraint) so duplicate or out-of-order updates can never double-credit escrow. Validate that the polled invoice's amount, currency, and `outOrderRef` match the local order before transitioning state. If signed webhooks are ever added as an optimization, the handler MUST cryptographically verify the `BTCPay-Sig` HMAC header before processing anything.
- **Ledger Integrity:** All ledger updates MUST be executed within a PostgreSQL transaction (`BEGIN`, `COMMIT`, `ROLLBACK`) to prevent race conditions. Ledger tables are append-only double-entry (see ARCHITECTURE.md §6); balances are never mutated in place.
- **Audit Trail:** Every moderator/admin action (dispute resolution, forced release/refund, vendor approval, withdrawal approval) writes an immutable audit-log row (actor, action, target, timestamp UTC, reason). Automated cron transitions are logged the same way with actor = `system`.
- **No Direct Payouts:** Never send BTC directly from an order checkout. All funds go to the platform escrow wallet; vendors withdraw via requests approved by an admin.

## 4. Anti-Phishing Measures (support & messaging)
Cloned .onion sites steal credentials by replaying our exact HTML; these controls help users
verify authenticity and slow automated phishing campaigns. See ARCHITECTURE.md §7–8.
- **Per-message anti-phishing CAPTCHA:** every private message and ticket reply requires a
  fresh, single-use visual challenge generated locally (no external services, no JS). The
  correct answer is derived from per-user state so a clone site cannot know it out-of-band.
- **Personal phrase:** each user sets a secret phrase rendered on their account pages and in
  support-ticket confirmations; if a "market" doesn't show it, the user knows the site is a
  clone. Prompt users to verify it before logging in or depositing.
- **Login history with mirror/host column:** record UTC timestamp + the `Host` header the
  login arrived on (never client IPs). Users see "which address did I just log in through?" —
  an unknown host string is a phishing red flag.
- **Inert URLs in messages:** user-posted links are displayed as plain text, never clickable,
  so clones can't harvest clicks and scam lures can't masquerade as market links.
- **Official-domain discipline:** publish the canonical .onion plus PGP-signed announcements
  (news board), a `canary.txt`, and a static `/pgp.txt`; warn against search-engine mirrors.
  Discourage third-party reverse proxies entirely (they are credential-harvesting machines).
- **Abuse reporting loop:** one-click "report message" → support ticket (category: vendor
  report/phishing); staff can suspend accounts; suspensions are audit-logged with reason.
- **Optional DDoS friction (documented, not default):** proof-of-work gates or image CAPTCHA
  on login during attacks. Keep any PoW logic tiny and auditable; never require JS.

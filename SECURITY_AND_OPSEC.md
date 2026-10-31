# Security & Operational Security (OpSec) Guidelines

## 1. Tor Hidden Service Specifics
- **Localhost Binding:** The Go web server MUST bind ONLY to `127.0.0.1:PORT`. Never bind to `0.0.0.0`.
- **No IP Rate Limiting:** Traditional IP-based rate limiting (like Fail2Ban) is useless here. Use Redis-based sliding window rate limiting keyed by `session_id` or `user_id`.
  - **Caveat:** anonymous attackers can mint fresh sessions to reset session-keyed limits. Wherever possible, key limits to the authenticated `user_id`; for pre-auth endpoints (login, registration, CAPTCHA), layer cheap friction first (Math CAPTCHA + honeypot + global throughput cap) so session-minting stays uneconomical.
- **Time Synchronization:** Do not implement custom NTP logic. Rely on the host OS. Do not leak timezones in HTTP headers.
- **Header Hygiene:** Strip/genericize the `Server:` header, never expose stack traces in error pages (render a generic template), and never register `pprof` or `expvar` routes. Serve uniform static assets (one CSS file, one font set, one image style) so per-page fingerprinting yields nothing.

## 2. Data Minimization & Privacy
- **No Analytics:** Do not implement Google Analytics, Plausible, or any tracking scripts.
- **Log Sanitization:** Ensure the Go logger strips out any potential PII. Log only errors and critical system events.
- **PGP Handling:** The server stores only user **public** keys and ciphertext. Private keys never touch the server; decryption of shipping addresses happens client-side on the vendor's machine. Process public-key import in memory, validate armored-block format, and never log raw key material or ciphertext.

## 3. Payment & Escrow Security
- **Payment Status via Polling (no webhooks):** A localhost-only hidden service cannot receive inbound webhooks from BTCPay (see ARCHITECTURE.md §2). The backend polls BTCPay's invoice API; every poll result is applied idempotently (unique `(invoice_id, status)` event key with a `UNIQUE` constraint) so duplicate or out-of-order updates can never double-credit escrow. Validate that the polled invoice's amount, currency, and `outOrderRef` match the local order before transitioning state. If signed webhooks are ever added as an optimization, the handler MUST cryptographically verify the `BTCPay-Sig` HMAC header before processing anything.
- **Ledger Integrity:** All ledger updates MUST be executed within a PostgreSQL transaction (`BEGIN`, `COMMIT`, `ROLLBACK`) to prevent race conditions. Ledger tables are append-only double-entry (see ARCHITECTURE.md §6); balances are never mutated in place.
- **Audit Trail:** Every moderator/admin action (dispute resolution, forced release/refund, vendor approval, withdrawal approval) writes an immutable audit-log row (actor, action, target, timestamp UTC, reason). Automated cron transitions are logged the same way with actor = `system`.
- **No Direct Payouts:** Never send BTC directly from an order checkout. All funds go to the platform escrow wallet; vendors withdraw via requests approved by an admin.

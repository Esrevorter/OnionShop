# Security & Operational Security (OpSec) Guidelines

## 1. Tor Hidden Service Specifics
- **Localhost Binding:** The Go web server MUST bind ONLY to `127.0.0.1:PORT`. Never bind to `0.0.0.0`.
- **No IP Rate Limiting:** Traditional IP-based rate limiting (like Fail2Ban) is useless here. Use Redis-based sliding window rate limiting keyed by `session_id` or `user_id`.
- **Time Synchronization:** Do not implement custom NTP logic. Rely on the host OS. Do not leak timezones in HTTP headers.

## 2. Data Minimization & Privacy
- **No Analytics:** Do not implement Google Analytics, Plausible, or any tracking scripts.
- **Log Sanitization:** Ensure the Go logger strips out any potential PII. Log only errors and critical system events.
- **PGP Handling:** When handling user PGP keys for shipping encryption, process them in memory and never log the raw key material.

## 3. Payment & Escrow Security
- **Webhook Verification:** The BTCPay webhook handler MUST cryptographically verify the `BTCPay-Sig` header before processing any payment updates.
- **Ledger Integrity:** All updates to the `vendor_ledger` table MUST be executed within a PostgreSQL transaction (`BEGIN`, `COMMIT`, `ROLLBACK`) to prevent race conditions.
- **No Direct Payouts:** Never send BTC directly from an order checkout. All funds go to the platform escrow wallet.

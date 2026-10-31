# Contributing to OnionShop

These rules mirror AGENTS.md and apply to human contributors as well.

## Non-negotiables
1. **No JavaScript** of any kind (no React/Vue/Alpine/HTMX), **no CDNs**, **no IP logging**.
2. All rendering through `html/template`; every POST form carries
   `<input type="hidden" name="csrf_token" value="{{.CsrfToken}}">`.
3. Cookies: `HttpOnly` + `Secure` + `SameSite=Strict` (HTTPS is terminated at nginx with a
   self-signed, SPKI-pinned .onion cert — see AGENTS.md §4, SECURITY_AND_OPSEC.md §1).
4. No browser-facing HSTS header (self-signed + HSTS = permanent lockout footgun; SPKI pinning in
   PGP-signed announcements is the authenticity mechanism). Security headers are defined in
   ARCHITECTURE.md §3 and must be emitted consistently by both nginx and Go middleware.
5. Payments: BTCPay status is obtained by **polling**, never webhooks (the hidden service is
   unreachable inbound). Every external event needs an idempotency key. See ARCHITECTURE.md §2.
6. DB access via `pgx`/`sqlc` (never GORM); all queries take a `context.Context`; ledger writes
   happen inside explicit Postgres transactions. Passwords: Argon2id with stored parameters.
7. Server stores only PGP **public** keys; never log PII, key material, or ciphertext. Logs go to
   a size-capped tmpfs/RAM buffer only — no log shipping off-host (SECURITY_AND_OPSEC.md §2).
8. CAPTCHAs are locally generated images (`img-src 'self'`) with single-use, session-bound
   tokens — never external services (hCaptcha/reCAPTCHA/Turnstile are CDNs). Honeypot fields
   supplement registration, ticket, and message forms. See AGENTS.md §3.
9. Support tickets & private messages follow ARCHITECTURE.md §7–8: message bodies render as
   escaped plain text, user-posted URLs stay inert (never auto-linked), staff actions on
   tickets/threads are audit-logged.
10. Infrastructure changes (nginx config, compose files, firewall/systemd units) must preserve the
    hardening baseline in SECURITY_AND_OPSEC.md §5: loopback-only listeners, default-deny egress,
    drop-all capabilities, read-only rootfs + tmpfs for volatile paths, Dropbear-over-onion admin
    (no OpenSSH, no clearnet SSH), LUKS2 at rest, no secrets in Git/images. Review these files like
    code; container images are pinned by digest.

## Dev workflow
```bash
make up          # Postgres + Redis (localhost-published ports only)
make migrate     # apply db/migrations
make test vet lint
```
CI enforces `go vet`, `staticcheck`, `golangci-lint`, `govulncheck`, and `go test`.
Keep TASKS.md checkboxes honest: mark `[x]` only when code is committed and building.

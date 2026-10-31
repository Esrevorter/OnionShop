# Contributing to OnionShop

These rules mirror AGENTS.md and apply to human contributors as well.

## Non-negotiables
1. **No JavaScript** of any kind (no React/Vue/Alpine/HTMX), **no CDNs**, **no IP logging**.
2. All rendering through `html/template`; every POST form carries
   `<input type="hidden" name="csrf_token" value="{{.CsrfToken}}">`.
3. Cookies: `HttpOnly` + `SameSite=Strict`, **no `Secure` flag on the .onion deployment**
   (plain HTTP inside the Tor circuit — browsers would drop such cookies). See AGENTS.md §4.
4. No HSTS header (no TLS ⇒ meaningless/dangerous). Security headers are defined in
   ARCHITECTURE.md §3.
5. Payments: BTCPay status is obtained by **polling**, never webhooks (the hidden service is
   unreachable inbound). Every external event needs an idempotency key. See ARCHITECTURE.md §2.
6. DB access via `pgx`/`sqlc` (never GORM); all queries take a `context.Context`; ledger writes
   happen inside explicit Postgres transactions. Passwords: Argon2id with stored parameters.
7. Server stores only PGP **public** keys; never log PII, key material, or ciphertext.
8. CAPTCHAs are locally generated images (`img-src 'self'`) with single-use, session-bound
   tokens — never external services (hCaptcha/reCAPTCHA/Turnstile are CDNs). Honeypot fields
   supplement registration, ticket, and message forms. See AGENTS.md §3.
9. Support tickets & private messages follow ARCHITECTURE.md §7–8: message bodies render as
   escaped plain text, user-posted URLs stay inert (never auto-linked), staff actions on
   tickets/threads are audit-logged.

## Dev workflow
```bash
make up          # Postgres + Redis (localhost-published ports only)
make migrate     # apply db/migrations
make test vet lint
```
CI enforces `go vet`, `staticcheck`, `golangci-lint`, `govulncheck`, and `go test`.
Keep TASKS.md checkboxes honest: mark `[x]` only when code is committed and building.

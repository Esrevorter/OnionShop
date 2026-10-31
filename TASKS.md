# Project Tasks & Roadmap

Status legend: `[x]` done and committed; `[ ]` not started.
(Previous revision incorrectly marked Phase 1 item 1 as done — no Go module existed in the
repository. Corrected 2026-10-31; documentation contradictions flagged during that review
were also fixed: Django-style CSRF syntax, `Secure` cookie requirement, HSTS, and BTCPay
webhooks. See AGENTS.md §3–4, ARCHITECTURE.md §2–3, SECURITY_AND_OPSEC.md §3.)

## Phase 1: Foundation
- [x] Initialize Go module (`go.mod`, `cmd/onionshop/main.go`) and directory structure
      (`internal/{server,store,cron}`, `web/templates`, `db/migrations`).
- [ ] Write `docker-compose.yml` (Postgres, Redis with `maxmemory-policy noeviction`).
- [ ] Write SQL migrations for the full schema (users/RBAC, products, orders, escrow,
      double-entry ledger with idempotency keys, disputes, audit log) via `golang-migrate`/`goose`.
- [ ] Set up DB connection pool (`pgx`) and Redis client; add `sqlc.yaml`.
- [ ] Add Makefile (`build/test/migrate/up/down`), `.gitignore`, `.editorconfig`.

## Phase 2: Core Server & Security
- [ ] Setup `chi` router and basic HTTP server (bind `127.0.0.1:8080` only).
- [ ] Implement Security Headers middleware per ARCHITECTURE.md §3
      (CSP, `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`; **no HSTS** —
      meaningless without TLS on a .onion origin).
- [ ] Implement Session management (`scs`): Redis store + Postgres fallback, ≥128-bit IDs from
      `crypto/rand`, rotation on login/privilege change, idle + absolute expiry, cookies
      `HttpOnly` + `SameSite=Strict` (no `Secure` flag — see AGENTS.md §4).
- [ ] Implement CSRF protection middleware (double-submit token rendered as
      `<input type="hidden" name="csrf_token" value="{{.CsrfToken}}">`).
- [ ] Implement Redis-based Rate Limiting middleware (key by `user_id` when authenticated;
      session-keyed limits only behind CAPTCHA/honeypot friction — see SECURITY_AND_OPSEC.md §1).

## Phase 3: Auth & RBAC
- [ ] User Registration & Login (Argon2id, params stored per-hash, dummy verify for unknown users).
- [ ] RBAC Middleware (Customer, Vendor, Mod, Admin).
- [ ] HTML Templates for Auth (with Math CAPTCHA & Honeypot).
- [ ] PGP public-key upload/validation (public keys only; private keys never server-side).

## Phase 4: Catalog & Vendors
- [ ] Vendor registration and Admin approval flow (writes to audit log).
- [ ] Product CRUD for vendors.
- [ ] Public storefront, search, and category browsing.

## Phase 5: Cart, Checkout & BTCPay
- [ ] Server-side session cart (Redis).
- [ ] BTCPay API client + invoice-status **poller** goroutine (no webhooks — localhost-only
      hidden service is unreachable inbound; see ARCHITECTURE.md §2). Idempotent event application.
- [ ] Checkout flow and QR code generation.
- [ ] Multi-vendor order splitting (N vendor-scoped orders per checkout).

## Phase 6: Escrow, Ledger & Disputes
- [ ] Ledger service (append-only double-entry credit/debit within Postgres transactions).
- [ ] Escrow state machine incl. failure paths (cancel/expiry, under/overpayment) and
      auto-finalization cron (UTC from Postgres, audit-logged as `system`).
- [ ] Dispute handlers and chat interface.
- [ ] Vendor withdrawal requests and Admin approval (audit-logged).

## Cross-Cutting / CI (start alongside Phase 1)
- [ ] CI pipeline: `go vet`, `golangci-lint`, `govulncheck`, `staticcheck`, `go test ./...`.
- [ ] Unit tests for escrow state-machine transitions first (highest-risk pure logic).
- [ ] Template tests asserting every POST form carries a `csrf_token` input.
- [ ] `CONTRIBUTING.md` restating AGENTS.md rules for human contributors.

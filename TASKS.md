# Project Tasks & Roadmap

Status legend: `[x]` done and committed; `[ ]` not started.
(Previous revision incorrectly marked Phase 1 item 1 as done — no Go module existed in the
repository. Corrected 2026-10-31; documentation contradictions flagged during that review
were also fixed: Django-style CSRF syntax, `Secure` cookie requirement, HSTS, and BTCPay
webhooks. Superseded 2026-11-01 by the hardened OpSec baseline (SECURITY_AND_OPSEC.md):
nginx reverse proxy + self-signed SPKI-pinned TLS on the onion is now the design, so cookies
DO carry `Secure` and CSP includes `upgrade-insecure-requests`; browser-facing HSTS remains
prohibited. OpenSSH is replaced by Dropbear over a private onion; tmpfs/LUKS2/default-deny
egress are mandatory.)

## Phase 1: Foundation
- [x] Initialize Go module (`go.mod`, `cmd/onionshop/main.go`) and directory structure
      (`internal/{server,store,cron}`, `web/templates`, `db/migrations`).
- [x] Write `docker-compose.yml` (Postgres, Redis with `maxmemory-policy noeviction` + persistence
      OFF, nginx reverse proxy) — hardened revision done 2026-11-01: UNIX-socket app↔nginx wiring
      (shared `appsock` volume, no TCP hop), `cap_drop: [ALL]` + `no-new-privileges` + `read_only`
      + tmpfs on every service, redisdata volume removed (RAM-only dataset), nginx publishes only
      `127.0.0.1:8443`, BTCPay isolated on its own network. Remaining gap (deliberate follow-up):
      images pinned by **digest** rather than minor tag.
- [x] Add `nginx/nginx.conf` to the repo (loopback-only listener, TLS 1.3/AEAD-only, static
      `/static/*` read-only mount, forwarding-header stripping, security headers matching the Go
      middleware — SECURITY_AND_OPSEC.md §5.1) + self-signed .onion cert generation script with
      SPKI-fingerprint output for pinning (§1) — see `scripts/gen-certs.sh`.
- [ ] Write SQL migrations for the full schema (users/RBAC, products, orders, escrow,
      double-entry ledger with idempotency keys, disputes, audit log) via `golang-migrate`/`goose`.
- [ ] Set up DB connection pool (`pgx`) and Redis client; add `sqlc.yaml`.
- [x] Add Makefile (`build/test/migrate/up/down` + `certs/install/compose-config` targets),
      `.gitignore`, `.editorconfig`.

## Phase 2: Core Server & Security
- [ ] Setup `chi` router and basic HTTP server (UNIX socket `/run/onionshop/app.sock` or
      `127.0.0.1:8080` only — never a public bind; existing localhost guard in `main.go` stays).
- [ ] Implement Security Headers middleware per ARCHITECTURE.md §3
      (CSP incl. `upgrade-insecure-requests`, `X-Content-Type-Options`, `X-Frame-Options`,
      `Referrer-Policy`; **no browser-facing HSTS** — self-signed + HSTS is a lockout footgun;
      headers re-asserted at nginx too).
- [ ] Implement Session management (`scs`): Redis store + Postgres fallback, ≥128-bit IDs from
      `crypto/rand`, rotation on login/privilege change, idle + absolute expiry, cookies
      `HttpOnly` + `Secure` + `SameSite=Strict` (HTTPS now terminates at nginx — see AGENTS.md §4).
- [ ] Implement CSRF protection middleware (double-submit token rendered as
      `<input type="hidden" name="csrf_token" value="{{.CsrfToken}}">`).
- [ ] Implement Redis-based Rate Limiting middleware (key by `user_id` when authenticated;
      session-keyed limits only behind CAPTCHA/honeypot friction — see SECURITY_AND_OPSEC.md §1).

## Phase 3: Auth & RBAC
- [x] User Registration & Login — password + CAPTCHA service layer done 2026-11-01:
      Argon2id hashing with self-describing per-hash param records (`internal/server/password.go`),
      constant-time dummy verify for unknown users, username/password validators;
      single-use HMAC-bound image CAPTCHA with Redis+memory mirror and deterministic
      stdlib PNG renderer (`internal/server/captcha.go`). Handlers/templates still pending.
- [ ] RBAC Middleware (Customer, Vendor, Mod, Admin) incl. account tiers (Basic/Upgraded).
- [ ] HTML Templates for Auth (with image CAPTCHA & Honeypot — see AGENTS.md §3).
- [ ] PGP public-key upload/validation + self-service PGP tool page (public keys only;
      private keys never server-side).
- [ ] Personal phrase (anti-phishing) + login-history table (UTC time + Host header, no IPs).

## Phase 4: Catalog & Vendors
- [ ] Vendor registration and Admin approval flow (writes to audit log).
- [ ] Product CRUD for vendors.
- [ ] Public storefront, search, and category browsing.
- [ ] Vendor profile pages (`/u/<handle>`): rating, sales count, ETA badge, "message vendor".
- [ ] Order reviews/ratings (one per order line, editable window, plain forms).
- [ ] Top-Vendors leaderboard cron snapshot + homepage widget.

## Phase 5: Cart, Checkout & BTCPay
- [ ] Server-side session cart (Redis).
- [ ] BTCPay API client + invoice-status **poller** goroutine (no webhooks — localhost-only
      hidden service is unreachable inbound; see ARCHITECTURE.md §2). Idempotent event application.
- [ ] Checkout flow and QR code generation.
- [ ] Multi-vendor order splitting (N vendor-scoped orders per checkout).
- [ ] Wallets page: per-currency available balance + in-escrow total, deposit/withdraw UI
      (XMR support gated on BTCPay store config; ledger accounts per currency).

## Phase 6: Escrow, Ledger & Disputes
- [ ] Ledger service (append-only double-entry credit/debit within Postgres transactions).
- [ ] Escrow state machine incl. failure paths (cancel/expiry, under/overpayment) and
      auto-finalization cron (UTC from Postgres, audit-logged as `system`).
- [ ] Dispute handlers and chat interface.
- [ ] Vendor withdrawal requests and Admin approval (audit-logged).

## Phase 7: Support Ticketing & Messaging (ARCHITECTURE.md §7–8)
- [ ] Migrations: `tickets`, `ticket_messages`, `threads`, `messages` (+ indexes, FKs).
- [ ] Support ticket CRUD: categories, statuses (`open/answered/user_replied/resolved/closed`),
      order linking, priority; create form with CSRF + honeypot + rate limit per `user_id`.
- [ ] Staff queue UI: filter/claim/reply/resolve with mandatory reason → audit log.
- [ ] Ticket ↔ dispute escalation (ticket → dispute; dispute resolution closes ticket).
- [ ] Private messaging: thread list, conversation view, unread-count nav badge (server-side).
- [ ] Image CAPTCHA service: local PNG generation, single-use tokens bound to session+thread,
      ~5 min expiry; required on every message POST (anti-phishing friction).
- [ ] Message abuse controls: inert-text URLs, no attachments, "report message" → ticket.
- [ ] Audited staff access to threads related to disputes/tickets.

## Phase 8: Community & Content (ARCHITECTURE.md §9; optional)
- [ ] News/announcements board (admin-authored, PGP-signable) + maintenance banner mode.
- [ ] Help center pages: phishing education, harm reduction, bug bounty, canary.txt, pgp.txt.
- [ ] Account upgrade flow (tiers gating features).
- [ ] Raffle system (ledger-backed entries, audited `crypto/rand` draws) — only if demand.

## Cross-Cutting / CI (start alongside Phase 1)
- [ ] CI pipeline: `go vet`, `golangci-lint`, `govulncheck`, `staticcheck`, `go test ./...`.
- [ ] Unit tests for escrow state-machine transitions first (highest-risk pure logic), then
      ticket status transitions and CAPTCHA token single-use/expiry.
- [ ] Template tests asserting every POST form carries a `csrf_token` input (message/ticket
      forms additionally assert CAPTCHA + honeypot fields).
- [ ] `CONTRIBUTING.md` restating AGENTS.md rules for human contributors.

## Infrastructure Hardening (SECURITY_AND_OPSEC.md §5 — host-level, tracked here so it stays honest)
- [ ] Provision host: LUKS2 (argon2id) root + data volumes, separate keyslots, swap off/encrypted,
      kernel sysctl hardening; Postgres/WAL and Tor `HiddenServiceDir` on the encrypted volume only.
- [ ] nftables default-deny INPUT/FORWARD/OUTPUT with process-scoped egress allowlist (tor user
      only; app/DB containers get none); DNS-leak canary checks; dropped-packet logs RAM-only.
      Repo template ready: `deploy/nftables.conf` (review for your NIC/tor-user setup before load).
- [ ] systemd sandboxes per service (`ProtectSystem=strict`, `PrivateTmp`, `NoNewPrivileges`,
      seccomp/AppArmor); tmpfs (`noexec,nosuid,nodev`) for `/tmp`, CAPTCHA PNGs, PGP import
      buffers, QR renders, secret env files. Repo templates ready: `systemd/onionshop-app.service`,
      `systemd/onionshop-nginx.service` (bare-metal path; compose path enforces equivalents).
- [ ] Dropbear over a private admin onion (key-only Ed25519, AEAD ciphers, no compression,
      15-min idle kill, RAM-only auth logs); OpenSSH uninstalled.
- [x] nginx config in-repo + self-signed .onion cert with SPKI fingerprint published in PGP-signed
      announcements and `pgp.txt` — `nginx/nginx.conf` + `scripts/gen-certs.sh` (prints the SPKI
      pin; warns about pin breakage on regeneration). Still open: images pinned by digest.
- [ ] Backup pipeline: `age`-encrypted dumps staged in tmpfs and shredded; quarterly off-site
      restore drills verifying ledger invariants; panic-wipe script tested (trigger restricted to
      admin channel/console only — never web-reachable). Scripts exist (`scripts/backup.sh`,
      `scripts/panic-wipe.sh` guarded by `--i-understand-this-is-irreversible` + hostname check);
      **drills still due** before any real value is held in escrow.
- [ ] Warrant canary automation: PGP-signed `canary.txt` refresh cadence + post-incident rotation
      runbook (TLS key, session key, API tokens).
- [ ] Guided installer for less-experienced operators: `install/install.sh` + `INSTALL.md` shipped;
      needs field-testing on a clean hardened host and per-distro OS-prep notes.

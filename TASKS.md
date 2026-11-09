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
- [x] Write SQL migrations for the full schema (users/RBAC, products, orders, escrow,
      double-entry ledger with idempotency keys, disputes+audit, sessions, auth phase-3) —
      done 2026-11-03: `db/migrations/0001..0006` (up/down pairs) under **golang-migrate**
      (`goose` was considered and dropped; Makefile `migrate`/`migrate-down` targets use the
      `migrate` CLI). Queries in `db/query/` verified by the CI `sqlc` job against `db/schema.sql`.
- [x] Set up DB connection pool (`pgx`) and Redis client; add `sqlc.yaml` — done 2026-11-03:
      `internal/store/` owns the pgxpool + go-redis clients (`FromEnv`, granular `ONIONSHOP_DB_*`
      fallbacks) and the durable scs sessions-fallback store; `sqlc.yaml` drives query generation
      into `internal/store/gen/` (gitignored), verified by the CI `sqlc` job.
- [x] Add Makefile (`build/test/migrate/up/down` + `certs/install/compose-config` targets),
      `.gitignore`, `.editorconfig`.

## Phase 2: Core Server & Security
- [x] Setup `chi` router and basic HTTP server — done 2026-11-03: `internal/server/router.go`
      (`App`/`Config`/`Deps`, recover middleware, `/healthz`) binds **only** a UNIX socket
      (`/run/onionshop/app.sock`, default) or loopback TCP (dev `-addr`); the localhost guard in
      `cmd/onionshop/main.go` rejects any non-loopback bind unless the compose-only contained-
      sandbox markers (`ONIONSHOP_NET_NEVER` + `ONIONSHOP_ADDR`) are both set.
- [x] Implement Security Headers middleware per ARCHITECTURE.md §3 — done 2026-11-03:
      `internal/server/headers.go` emits CSP (incl. `upgrade-insecure-requests`, `script-src 'none'`),
      `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`;
      **no browser-facing HSTS** (self-signed + HSTS is a lockout footgun); `StripForwardedHeaders`
      drops client-supplied forwarding headers; headers re-asserted at nginx (`nginx/nginx.conf`).
- [x] Implement Session management (`scs`) — done 2026-11-03: `internal/server/sessions.go` +
      `internal/store/sessions.go`: Redis-backed store with durable Postgres fallback
      (`FallbackStore`), ≥128-bit tokens from `crypto/rand`, rotation on login/privilege change
      (`RotateOnAuth`), idle + absolute expiry (`AbsoluteExpiry`), cookies `HttpOnly` + `Secure`
      (`ONIONSHOP_SECURE_COOKIES=false` for local dev only) + `SameSite=Strict`.
- [x] Implement CSRF protection middleware — done 2026-11-03: double-submit token in the session
      (`CSRFLoad`/`CSRFProtect`), rendered as `<input type="hidden" name="csrf_token"
      value="{{.CsrfToken}}">` in every auth form; safe methods pass through, mismatches get a
      generic error page.
- [x] Implement Redis-based Rate Limiting middleware — done 2026-11-03: `internal/server/ratelimit.go`
      (token buckets in Redis with in-memory mirror, hashed keys — no raw identities), keyed by
      `user_id` when authenticated; session/IP-free anonymous limits apply only behind the
      CAPTCHA/honeypot friction path (SECURITY_AND_OPSEC.md §1).

## Phase 3: Auth & RBAC
- [x] User Registration & Login — password + CAPTCHA service layer done 2026-11-01:
      Argon2id hashing with self-describing per-hash param records (`internal/server/password.go`),
      constant-time dummy verify for unknown users, username/password validators;
      single-use HMAC-bound image CAPTCHA with Redis+memory mirror and deterministic
      stdlib PNG renderer (`internal/server/captcha.go`).
      Handlers/templates done 2026-11-04: `internal/server/auth_handlers.go` registers
      GET/POST `/register` + `/login` (CAPTCHA + honeypot friction per AGENTS.md §3, session
      rotation on auth, transparent argon2 param upgrade), go:templates under `web/templates`
      (`page_home/login/register/account/pgp.html` + shared `form_friction.html`, parsed
      fail-loud at boot via `buildTemplates`;
      template dir overridable with `ONIONSHOP_TEMPLATE_DIR`), mounted by `RegisterAuthRoutes`
      in `cmd/onionshop/main.go`. Covered by `auth_handlers_test.go` (fake AuthStore, full
      login-chain history assertions, template-parsing failure modes).
- [x] RBAC Middleware (Customer, Vendor, Mod, Admin) — role gate `RequireRole` mounted on
      `/account` and wired through the router 2026-11-04; roles are read from the session at
      login. Account tiers (Basic/Upgraded) remain pending (depends on Phase 4 vendor flow).
- [x] HTML Templates for Auth (with image CAPTCHA & Honeypot — see AGENTS.md §3) — done
      2026-11-04 together with the handlers above (inline CAPTCHA PNG + hidden single-use
      token + honeypot field in every auth form).
- [x] PGP public-key upload/validation + self-service PGP tool page (public keys only;
      private keys never server-side) — done 2026-11-04: `internal/server/pgp.go` validates
      armored PUBLIC keys in memory via `golang.org/x/crypto/openpgp` (private blocks and
      malformed armor rejected before any DB write; raw key material never logged), computes
      the display fingerprint, and serves the public explainer at GET `/pgp`
      (`page_pgp.html`). Upload/clear POSTs (`/account/pgp`, `/account/pgp/clear`) sit behind
      the account gate + CAPTCHA/honeypot friction with fresh-challenge re-render on failure;
      storage via `AuthGetPGPKey`/`AuthSetPGPKey`/`AuthClearPGPKey` (migration 0001 column).
      Fixed same day: `parseUUID` no longer relies on `pgtype.UUID.Scan` (canonical-string
      decode), which had made identity-dependent POSTs fail closed with a silent 500.
      Covered by `pgp_test.go` + `TestPGPUploadAndClearFullChain` in `auth_handlers_test.go`.
- [x] Personal phrase (anti-phishing) + login-history table (UTC time + Host header, no IPs) —
      done 2026-11-04: successful logins record `(user_id, UTC timestamp, r.Host)` via the
      4-arg `AuthRecordLogin(ctx, pgtype.UUID, time.Time, string)`; a history-write failure is
      logged non-fatally and never blocks the login (still 303 → `/account`) — asserted by
      `TestLoginRecordsHistoryWithUTCTimeAndHost` / `TestLoginHistoryWriteErrorDoesNotBlockLogin`.

## Phase 4: Catalog & Vendors
- [x] Vendor registration and Admin approval flow (writes to audit log).
- [x] Product CRUD for vendors.
- [x] Public storefront, search, and category browsing.
- [x] Vendor profile pages (`/u/<handle>`): rating, sales count, ETA badge, "message vendor".
- [x] Order reviews/ratings (one per order line, editable window, plain forms).
- [x] Top-Vendors leaderboard cron snapshot + homepage widget.

## Phase 5: Cart, Checkout & BTCPay
- [x] Server-side session cart (Redis).
- [x] BTCPay API client + invoice-status **poller** goroutine (no webhooks — localhost-only
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
- [x] CI pipeline — done 2026-11-03: `.github/workflows/ci.yml` runs `go vet`, `golangci-lint`
      (pinned v1.64.8, Go 1.25.x toolchain pinned + integrity assert), `go test ./...`, a
      `govulncheck` job (informational until the base image bump), and an `sqlc generate` job;
      `staticcheck` remains in `make lint`.
- [ ] Unit tests for escrow state-machine transitions first (highest-risk pure logic), then
      ticket status transitions and CAPTCHA token single-use/expiry — CAPTCHA single-use/expiry
      **done 2026-11-03** (`captcha_test.go`); escrow/ticket transition tests still open (their
      services are stubs until Phases 6–7).
- [ ] Template tests asserting every POST form carries a `csrf_token` input (message/ticket
      forms additionally assert CAPTCHA + honeypot fields) — auth-form friction is covered today
      inside `auth_handlers_test.go`; a dedicated repo-wide template sweep is still open.
- [x] `CONTRIBUTING.md` restating AGENTS.md rules for human contributors — done 2026-10-31.

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

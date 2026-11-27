# TASKS.md

# Project status and roadmap

`[x]` means an application feature exists, not that it is security-certified or production-operated.

## Application feature status

Phases 1–8 application features are implemented. The initial FIXME checklist's statements that these were pending were stale.

## Open engineering and production gates

- [ ] Expand high-risk escrow transition/template coverage and complete isolated integration tests.
- [ ] Verify external image digests/platforms; some compose images remain mutable tags.
- [ ] Keep Go toolchain rationale accurate: Go 1.25 is the repository's project pin, not an asserted pgx minimum.
- [ ] Review ProtonMail go-crypto dependency maintenance/security posture.
- [ ] Verify the self-signed ECDSA P-384 certificate has exact DNS SAN for actual v3 onion hostname; test exact supported Tor Browser version on clean profile. Never use a CA-issued cert, SPKI/hash pin, or warning bypass. Onion protocol provides onion identity; TLS key is separate.
- [ ] Verify Tor/host/Compose port mapping, UNIX socket permissions/health checks, service egress/DNS, and strict Whonix-Gateway/AppVM network isolation (ensuring the AppVM has no physical NIC and all traffic routes through the Gateway).
- [ ] Complete disposable-host LUKS/firewall/systemd/Dropbear and recovery tests.
- [ ] Complete offline-key backup restore and establish canary/incident ownership.

## Phase 9: Reliability & Safety Net

> **Rationale.** Phase 10 (Monero settlement) is the single highest-consequence change in the
> project — money flows, escrow state transitions, vendor payouts. It must ship on a foundation
> that is proven working end-to-end and that does not silently degrade under stress. The dev-stack
> bring-up (DEVLOG.md §1) demonstrated empirically that the current CI gate (`go test ./...`
> against in-memory fakes) catches none of the integration bugs that actually ship: fourteen
> fatal or functionally disabling defects reached `main` before the first successful container
> boot. Adding the settlement layer to that uncaptured pipeline risks the expensive class of bug
> this project is designed to prevent.
>
> **Why now, not after.** Reliability work after Phase 10 inherits it — money bugs built on a
> silently-degrading foundation are the worst possible combination. Resilience work before Phase
> 10 gives the settlement layer the safety net it deserves. This phase also adds **zero user-facing
> change**: no template, route, or feature moves, so end-user UX is untouched while both admin
> UX (defect 12: silent degradation after reboot) and end-user UX (uptime) are directly protected.
>
> **Relationship to existing gates.** This phase resolves defects 12–25 from DEVLOG.md §1 and
> the §5.6 gaps (resource ceilings, image pinning) noted earlier in that log. It does **not**
> touch any application logic from Phases 1–8; all fixes are in infrastructure, middleware and
> observability.

### 9.0 P0 — Correctness (blocks everything)
- [ ] Confirm `go test ./...` is green. The five CAPTCHA-marker failures
      (`TestLoginRecordsHistoryWithUTCTimeAndHost`, `TestLoginHistoryWriteErrorDoesNotBlockLogin`,
      `TestLoginHoneypotSilentlyRejected`, `TestPGPUploadAndClearFullChain`,
      `TestPGPUploadStoreFailureRendersFreshChallenge`) are **likely still red** on `main` — the
      fixes from DEVLOG.md §1 Defect 24 (`postForm` reads prompt via `CaptchaService.Challenge()`,
      `extractText` removed, `friction_contract_test.go` added) need verification. If not landed,
      apply them; nothing else in this phase matters while the suite is red.
- [ ] Add the CI smoke gate to `.github/workflows/ci.yml`: a fourth job, `smoke`, sibling to
      `go`/`vuln`/`sqlc`. Boots the real compose stack, applies migrations against the container
      IP, then asserts:
      - `curl -sfk https://127.0.0.1:8443/health` returns `ok`
      - `GET /login` contains `name="captcha_token"` (friction contract)
      - `/static/main.css` returns 200
      - HTTP 8080 redirects to HTTPS 443
      - `X-Frame-Options:` count on `/login` is exactly 1 (catches defect 16)
      - TLS 1.2 is refused (catches defect 10 regression)
      Uses an ephemeral `.env` generated with `{ echo …; } > .env` (not heredoc, to avoid
      whitespace in secrets); placeholder TLS material with `sudo chgrp 101`; services named
      explicitly so `backup`/`btcpay` never start. On failure: `docker compose logs --tail=100`.
      On success: `docker compose down -v --remove-orphans`.
- [ ] Add a regression test inside `smoke`: stop postgres, restart the app, assert the container
      becomes **unhealthy** rather than healthy-but-crippled. Catches defect 12 recurrence.

### 9.1 P1 — Resilience (uptime = user trust)
- [ ] Defect 12: fail fast on missing infrastructure. `main.go` currently logs
      `"postgres unavailable (degraded mode)"` and continues with a nil pool, which causes
      `RegisterAuthRoutesFull` to silently skip mounting auth/catalog/vendor/admin routes. A
      Docker daemon restart (which ignores `depends_on`) or a host reboot can leave the market
      serving only anonymous 404 redirects indefinitely, with only a log line to indicate it.
      Two acceptable fixes, pick one:
      - **Fail fast:** `log.Fatal` when `OpenDBFromEnv` or `OpenRedisFromEnv` fails; let
        `restart: unless-stopped` retry until dependencies are ready. Simplest; matches
        "no silent degradation" from AGENTS.md §3.
      - **Bounded retry:** loop for up to ~60s with backoff before giving up, then register
        routes. Keeps the degraded-mode philosophy but bounds it.
      I recommend fail-fast. A market that cannot authenticate anyone should not be reporting
      healthy.
- [ ] Defect 13: make `/healthz` a genuine readiness probe. Currently it pings Redis only. It
      must also assert the Postgres pool is non-nil and the authenticated routes are mounted. The
      latter is the meaningful check — when the pool fails, routes stay nil, and a market
      reporting "healthy" while unable to log anyone in is worse than one reporting "unhealthy".
- [ ] Defect 15: stale socket unlink. `net.Listen("unix", …)` does not remove a pre-existing
      socket file; after any SIGKILL, daemon restart or host reboot the app fails with
      `EADDRINUSE` and loops. In `internal/server/router.go` `Serve`, before `net.Listen`, add:
      ```go
      if fi, err := os.Stat(a.cfg.UnixSocket); err == nil && fi.Mode()&os.ModeSocket != 0 {
          if err := os.Remove(a.cfg.UnixSocket); err != nil {
              return fmt.Errorf("server: remove stale socket: %w", err)
          }
      }
      ```
      The `ModeSocket` guard is essential — without it a typo'd `ONIONSHOP_UNIX` pointing at a
      real file would silently delete it. Needs `os` imported.
- [ ] Defect 14: misleading startup log. `main.go` unconditionally prints
      `onionshop listening on <TCP addr> (TCP; bind policy enforced by resolveAddr)` even when
      `Serve` bound the UNIX socket. Replace with the actual listener: log the socket path when
      `a.cfg.UnixSocket` is set, the TCP address otherwise.

### 9.2 P2 — Security config (zero UX cost)
- [ ] Defect 16: duplicate security headers. Go middleware emits them, nginx `add_header`
      appends — every response currently carries `X-Frame-Options`, `X-Content-Type-Options`,
      `Referrer-Policy` and `Content-Security-Policy` **twice**. Duplicate `X-Frame-Options` has
      undefined behaviour; some user agents ignore the header entirely, silently removing
      clickjacking protection. In `nginx/nginx.conf` 8443 server block, before the `add_header`
      lines:
      ```nginx
      proxy_hide_header Content-Security-Policy;
      proxy_hide_header X-Frame-Options;
      proxy_hide_header X-Content-Type-Options;
      proxy_hide_header Referrer-Policy;
      proxy_hide_header Permissions-Policy;
      ```
      Verify with `curl -sk -D - -o /dev/null https://127.0.0.1:8443/login | grep -ci
      '^x-frame-options:'` — must print `1`, not `2`.
- [ ] Defect 17: `/static/` location inheritance. nginx's `add_header` inheritance rule: a
      `location` that defines any `add_header` does not inherit the parent's list. The
      `/static/` block has `add_header Cache-Control "public, immutable"`, so it likely drops
      CSP, XFO and the rest. Verify, then either repeat the security header set inside that
      location or move the common set into an `include` fragment used by both.
- [ ] Defect 18: correct the §5.1 claim about stripping `Server:`. `server_tokens off` hides the
      version, not the header; `proxy_hide_header Server` strips only the upstream's header.
      Responses still carry `Server: nginx`. Update SECURITY_AND_OPSEC.md §5.1 to state the
      achievable floor — `Server: nginx` with no version — rather than implying full removal.

### 9.3 P3 — Toolchain & DX (prevents future drift)
- [ ] Defect 20: pin staticcheck. `make lint` calls bare `staticcheck` from `$PATH`. Latest
      staticcheck (`honnef.co/go/tools@v0.8.2`) requires Go ≥ 1.26; CI pins golangci-lint
      v1.64.8 which bundles `honnef.co/go/tools v0.6.1`. Install `staticcheck@v0.6.1` locally
      (matching what golangci-lint already runs) to make local lint agree with CI.
- [ ] Defect 21: `make lint` uses the same
      `go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run --timeout 5m ./...`
      invocation as CI, with `GOTOOLCHAIN=local` set. Removes the drift where a locally-installed
      binary disagrees with CI.
- [ ] Defect 22: reconcile Makefile `DATABASE_URL` default. Makefile hardcodes
      `postgres://onionshop:onionshop_dev_only@127.0.0.1:5432/onionshop` while `install.sh`
      generates a random `DB_PASSWORD` into `.env`. `make migrate` silently authenticates with a
      credential that cannot work. Either:
      - commit a `.env.dev` with the fixed `onionshop_dev_only` credentials for local work, or
      - change the Makefile default to require `DATABASE_URL` from the environment and fail
        loudly when unset.
      The second option is more robust; the first preserves the current UX for developers who
      don't want to source `.env` every time.
- [ ] Defect 23: `make sqlc` is an undocumented prerequisite for `docker compose build app`.
      `internal/store/gen/` is gitignored and the Dockerfile `COPY`s `internal/` — a clean clone
      fails to build with undefined symbols. Either:
      - document the prerequisite in INSTALL.md and AGENTS.md, or
      - add `go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 && sqlc generate` to the build
        stage of the Dockerfile so the image is self-sufficient.
      The second option removes the failure mode; the first keeps the build fast on the host.
- [ ] §5.6 gaps noted in DEVLOG.md: add `mem_limit`/`pids_limit` to every compose service
      (matching the systemd units' `MemoryMax=1500M`/`TasksMax=256`); pin every base image by
      digest (`sha256:…`) rather than mutable minor tag (`alpine:3.19`, `nginx:1.27-alpine`,
      `postgres:16-alpine`, `redis:7-alpine`). Both are currently absent from the tracked
      compose file.

### 9.4 Definition of done
- [ ] `make test` green and CI green, including the five previously-failing full-chain tests
      and the new smoke job.
- [ ] The smoke gate fails if any of the P1 fixes are reverted (reproduces the degraded
      behaviour and asserts the container goes unhealthy).
- [ ] `systemctl restart docker` (or a reboot) leaves the stack converging to `healthy`
      **unattended** — no manual `docker compose restart app` needed.
- [ ] A fresh clone following INSTALL.md reaches `ok` on `/health` without any tribal knowledge
      from this session.

### 9.5 What this phase explicitly does NOT do
- [ ] **Does not start Phase 10 (Monero settlement).** Phase 10 is the highest-consequence work
      in the project and must ship on a proven foundation. This phase is the gate for that.
- [ ] **Does not change any application feature from Phases 1–8.** No template, route, handler
      or schema migration in this phase. End-user UX is untouched by design.
- [ ] **Does not alter the baseline Whonix topology.** The Whonix-Gateway / Whonix-Workstation (AppVM) split fronting the `app`, `postgres`, `nginx`, `redis`, and `backup` servers is the established baseline architecture for strict network isolation, not a Phase 10 addition.

---


### 9.6 Evidence snapshot (2026-11-24)
Computed from the working tree, not asserted:
- [x] stale socket unlink (defect 15) (evidence: `ModeSocket` in `internal/server/router.go`)
- [x] duplicate headers hidden (defect 16) (evidence: `proxy_hide_header` in `nginx/nginx.conf`)
- [x] fail-fast on missing DB (defect 12) (evidence: `log.Fatal` in `cmd/onionshop/main.go`)
- compose: 7 mem_limit, 7 pids_limit, 1 digest-pinned images (tag-only images remain an open gate).

## Phase 10: Monero Settlement Layer — SETTLED

> The original MoneroPay plan is archived at
> [docs/archive/PHASE10_MONEROPAY_PLAN.md](docs/archive/PHASE10_MONEROPAY_PLAN.md).
> It is historical rationale, not an active TODO.

### Shipped
- [x] BTCPay placeholder services, credentials, volume and egress hole removed.
- [x] Own `monerod` node in compose; internal network only.
- [x] `monero-wallet-rpc` internal-only, **no published ports**.
- [x] In-house JSON-RPC client: `create_address` per order, `get_transfers` observation.
      MoneroPay deliberately not adopted (one fewer daemon, no unauthenticated spend API).
- [x] Polling only; no callbacks/webhooks anywhere.
- [x] Integer cents/piconero end to end; `parseDecimalUnits` / `parseDecimalUnitsAllowZero`
      replace every float conversion in the money path; unit tests cover the 12-decimal
      round-trip, precision rejection and int64 overflow.
- [x] Exact-amount confirmation on the order's own subaddress; overpayment flagged for
      manual review without state change; underpayment records the observed delta.
- [x] Per-order subaddress never rebound; expiry handled by our cron, receiver retained.
- [x] Refunds and withdrawals broadcast on-chain and persist `tx_hash`; ledger legs idempotent.
- [x] Buyer refund address captured at checkout; delivery details PGP-encrypted to the vendor.
- [x] Native 6-decimal XMR display, copy-selectable address blocks, no JS.
- [x] Documentation reconciled (ARCHITECTURE, SECURITY_AND_OPSEC, UI, INSTALL, README,
      PRODUCTION_READINESS); SPKI-pinning contradiction resolved across README/AGENTS.

### Open before mainnet exposure
- [ ] Airgapped **view-only** wallet ceremony (address + private view key + restore height);
      spend key never on the server; payouts signed offline.
- [ ] Confirmation policy scaled by value; reject `double_spend_seen` and unelapsed
      `unlock_time`; honour the 10-block spendable lock.
- [ ] Value-cap CHECK on atomic columns; `NUMERIC` for whole-ledger aggregates
      (total XMR supply exceeds int64).
- [ ] End-to-end rehearsal on **stagenet**: order → pay → escrow → ship → release →
      withdrawal, plus refund and dispute paths.
- [ ] Host/CI assertions: every compose network `internal: true`; zero `ports:` on
      wallet-rpc; zero DNS egress; swap absent or LUKS-backed.
- [ ] Node sync expectations and "invoices unavailable until synced" gate in INSTALL.md.
- [ ] FCMP++/Carrot evaluation when it activates; revisit confirmation policy then.

See [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md); a running local stack is not
production clearance.


## Doc reconciliation snapshot (2026-11-27, computed from the working tree)
- [x] Honeypot randomization closed: `HONEYPOT_FIELD` env -> validated in
      `internal/server/auth_handlers.go` -> `HoneypotField` template func.
- [x] Two-step personal-phrase login shipped (`AuthPhraseByUsername`,
      `sess.login_phrase`; SECURITY_AND_OPSEC.md section 4.2).
- [x] Ledger hardening migration 0023 shipped, incl. `DROP VIEW IF EXISTS
      account_balances` fix for the 0009 `balance_sat` -> `balance_piconero`
      view-column drift (a plain recreate fails with "cannot change name of
      view column").
- [x] FTS: tsvector+GIN shipped (0001 index `products_search_idx`,
      `plainto_tsquery` in catalog_store.go / qProductsSearch); `pg_trgm`
      typo tolerance remains open.
- [x] Redis `CONFIG` renamed (docker-compose.yml); Tor PoW enabled
      (deploy/torrc); NET_RAW absent from the app service.
- [ ] Still open: least-privilege `onionshop_app` DB role bootstrap (+ REVOKE),
      TOTP enrollment UX (otpauth URI/QR), text CAPTCHA alternative,
      register-path enumeration review, catalog tests, CI smoke gate, and the
      Phase 9 items above.
- Note: ARCHITECTURE.md, AGENTS.md, UI.md and INSTALL.md are referenced by older
      documents but are not tracked in this repo; their content lives in README,
      SECURITY_AND_OPSEC and CONTRIBUTING. Do not re-add links to missing files.

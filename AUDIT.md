# Audit and Action Plan

This document serves as an independent review of the repository's current state, cross-referencing the architectural intent documented in `ARCHITECTURE.md`, `SECURITY_AND_OPSEC.md`, and `AGENTS.md` against the actual implementation in `nginx/nginx.conf`, `docker-compose.yml`, `deploy/torrc`, and the Go codebase. It also provides a concrete implementation guide for achieving a modern, no-JS UI.

---

## 1. Critical Configuration Contradictions (Fix First)

These items represent conflicts where the documentation explicitly forbids a configuration, yet the shipped files implement it (or vice versa), resulting in broken deployments or violated constraints.

- [x] **Remove HSTS from Nginx:** `nginx.conf` currently emits `Strict-Transport-Security`. This violates the explicit "no browser-facing HSTS" rule in `AGENTS.md` §4, `CONTRIBUTING.md` #4, `ARCHITECTURE.md` §3, `SECURITY_AND_OPSEC.md` §1, and `internal/server/headers.go`. With a self-signed cert, HSTS creates a permanent, non-bypassable lockout if the cert is ever regenerated (breaking the SPKI pin).
  - **Action:** Delete the `add_header Strict-Transport-Security ...` line from `nginx.conf`.

- [x] **Fix Nginx/Compose Port Binding & Reachability:** As shipped, `deploy/torrc` maps `HiddenServicePort 80 → 127.0.0.1:8443` (TLS), but `nginx.conf` has no plain-HTTP listener (meaning `http://` fails), and port `443` is unmapped (meaning `https://` fails). Furthermore, `nginx.conf` binds `listen 127.0.0.1:8443` *inside the container*, but compose publishes the host port to the container's bridge interface, not loopback.
  - **Action:** Implement the redirect listener in `nginx.conf` (`listen 8080` returning 301 to `https://`), add `HiddenServicePort 443 → 127.0.0.1:8443` to `deploy/torrc`, and change `listen 127.0.0.1:8443 ssl;` to `listen 8443 ssl;` in `nginx.conf` so the compose publisher can reach it.

- [x] **Unify Content Security Policy (CSP):** `headers.go` (matching `ARCHITECTURE.md` §3) uses `script-src 'none'` and `style-src 'self' 'unsafe-inline'`. `nginx.conf` uses `script-src 'self'` and `style-src 'self'` (no `unsafe-inline`). Because nginx `add_header` *adds* headers rather than overwriting them, browsers receive both and enforce the intersection, blocking all inline styles and violating the absolute no-JS rule (`script-src 'self'` allows JS execution).
  - **Action:** Make the CSP string in `nginx.conf` byte-identical to the constant in `internal/server/headers.go`. Add a CI test that diffs the two strings.

- [x] **Reconcile UNIX Socket vs. TCP:** Five documents (`README`, `ARCHITECTURE` §1, `SECURITY` §5.1, `INSTALL` §6, `TASKS` Phase 1) mandate a UNIX socket (`app.sock`) with no TCP hop. The actual `docker-compose.yml` and `nginx.conf` use a TCP bridge (`upstream { server app:8080; }`).
  - **Action:** Either implement the shared `appsock` volume in compose and update `nginx.conf` to `proxy_pass http://unix:/run/onionshop/app.sock;`, OR update all five markdown files to reflect the internal TCP bridge reality.

- [x] **Fix Backup Sidecar Egress:** The `backup` container in `docker-compose.yml` has an entrypoint that runs `apk add postgresql16-client age` at boot. However, the service is on `internal_net` (`internal: true`), meaning it has no internet access and will fail to start.
  - **Action:** Remove the runtime `apk add`. Bake `pg_dump` and `age` into the backup image via a multi-stage `Dockerfile.backup`.

- [x] **Resolve BTCPay Stub & Egress Rules:** **[SUPERSEDED by ADR-002: the services were removed entirely, not completed.]** The BTCPay container violates the "no floating tags" rule (`latest`), lacks NBXplorer/bitcoind for on-chain tracking, and needs blockchain egress, which conflicts with the host's "egress only for the tor uid" nftables policy.
  - **Action:** Pin the image to a digest, remove the Bitcoin stack entirely (superseded by ADR-002), and document the Tor-routed sync path for the host firewall.

---

## 2. Security & OpSec Gaps

- [ ] **Implement 2FA (TOTP):** Add server-rendered QR code generation at enrollment and a 6-digit TOTP field for Vendor, Mod, and Admin accounts. Ensure printable recovery codes are generated.
  - **Partial (verified 2026-11-27):** the user model stores `TOTPSecret`, `TOTPEnabled`, and `BackupCodes` (`internal/server/auth_handlers.go`), so verification at login is wired; the missing piece is the enrollment UX — no `otpauth://` provisioning URI or server-rendered QR exists anywhere in `internal/server/`. Also confirm the role gate actually requires TOTP for Vendor/Mod/Admin, not just optional buyer enrollment.
- [x] **Define Account Recovery SOP:** Support-desk social engineering is the #1 account takeover vector. Define a strict PGP-signed recovery procedure (or an explicit "no recovery" policy) and ensure every step is audit-logged.
  - **Closed:** `SECURITY_AND_OPSEC.md` §4.1 defines the SOP — no silent recovery, PGP-signed privileged-role recovery verified against the stored pubkey fingerprint, explicit no-recovery for Buyer/Vendor with balance-handling rules, and mandatory audit logging of every step including refusals.
- [x] **Implement Two-Step "Personal Phrase" Login:** `SECURITY_AND_OPSEC.md` §4 suggests users verify their phrase *before* logging in, but currently, it only renders on the `/account` page (post-auth).
  - **Action:** Implement a two-step login. `GET /login` takes a username; the server renders the user's personal phrase alongside the password field.
  - **Closed:** `POST /login` step 1 (username only) looks up the phrase via `PGAuthStore.AuthPhraseByUsername` (`COALESCE(personal_phrase, anti_phishing_phrase)`; unknown/phrase-less users both return `""` → no enumeration signal), stashes it in the session (`sess.login_phrase`/`sess.login_user`) and re-renders `page_login.html` showing the phrase before the password field. Legacy single-POST clients still authenticate (progressive UX, equal argon2 timing preserved). See `SECURITY_AND_OPSEC.md` §4.2.
- [x] ~~Publish Both Certificate Fingerprints~~ — **SUPERSEDED by ADR-001** (no certificate/SPKI fingerprints are published; see docs/DECISIONS.md). Original finding: Tor Browser's cert viewer shows the *certificate's* SHA-256 fingerprint, not the Subject Public Key Info (SPKI) hash. Ordinary users cannot verify the SPKI pin.
  - **Action:** Update `pgp.txt` and announcements to include **both** the Cert SHA-256 and the SPKI SHA-256, with a screenshot guide for Tor Browser users.
  - **Correction (verified 2026-11-27):** that action is void — publishing pins contradicts ADR-001 (`docs/DECISIONS.md`). Verification is behavioural: clean Tor Browser profile, exact supported build, cert DNS SAN equals the v3 `.onion` hostname, per `PRODUCTION_READINESS.md`.
  - **Correction (verified 2026-11-27):** the action above is void � publishing pins contradicts ADR-001 (`docs/DECISIONS.md`). Verification is behavioural: clean Tor Browser profile against the exact supported build, cert SAN equals the v3 `.onion` hostname, per `PRODUCTION_READINESS.md`.
- [x] **Enable Tor Proof-of-Work (PoW) DoS Defense:** Onion services are vulnerable to rendezvous circuit exhaustion.
  - **Action:** Add `HiddenServicePoWDefensesEnabled 1`, `HiddenServicePoWQueueRate 25`, and `HiddenServicePoWQueueBurst 50` to `deploy/torrc` (requires Tor >= 0.4.8).
- [x] **Enforce Ledger Immutability at the DB Layer:** The ledger is described as "append-only," but the application user currently has `UPDATE`/`DELETE` privileges on ledger tables.
  - **Action:** `REVOKE UPDATE, DELETE ON ledger_lines FROM onionshop_app;`. Add a Postgres trigger to assert that debits equal credits for every transaction.
  - **Closed (with scope note):** `db/migrations/0023_ledger_hardening.up.sql` recreates the drifted `account_balances` view (0009 renamed `amount_sat` → `amount_piconero` without `CREATE OR REPLACE VIEW`), adds a deferrable constraint trigger asserting per-`tx_ref` debits = credits at COMMIT, and re-asserts the append-only `BEFORE UPDATE OR DELETE` trigger from 0003. The literal `REVOKE` was intentionally *not* run: no `onionshop_app` role exists anywhere in `db/` or `install/` — the app connects as the schema owner, so a REVOKE would be a silent no-op while *appearing* hardened. A least-privilege role bootstrap is tracked in TASKS.md.
- [ ] **Improve CAPTCHA Accessibility & Honeypot Security:** Visual-only CAPTCHAs exclude users. The honeypot field name (`sta`) is public in the open-source repo.
  - **Action:** Add a text/logic alternative to the CAPTCHA. Randomize the honeypot field name per-deployment via `.env` at install time, and add `aria-hidden="true" tabindex="-1" autocomplete="off"`.
  - **Partial (honeypot closed):** `HONEYPOT_FIELD` is generated by `install/install.sh` (`openssl rand -hex 6`), injected via `docker-compose.yml`, validated in `internal/server/auth_handlers.go` (fallback `sta` + log on invalid), and rendered through the `HoneypotField` template func with `aria-hidden="true" tabindex="-1" autocomplete="off"` in all five templates. Remaining: text/logic CAPTCHA alternative (`AuthData.CaptchaTextAlt` flag exists; full alternate challenge not yet shipped).
- [ ] **Prevent Username Enumeration on `/register`:** Ensure the registration endpoint returns a uniform success/error message (or uses email/PGP verification links) so attackers cannot harvest valid usernames via timing or error responses.
  - **Note (verified 2026-11-27):** the two-step login path is enumeration-safe by construction (`AuthPhraseByUsername` returns `""` for unknown and phrase-less users alike). This item covers only the **register** path � "username taken" responses there still need review for a uniform shape/timing.
- [x] **Remove Unnecessary Linux Capabilities:** The `app` container has `cap_add: [NET_RAW]`. Busybox `wget` does not require raw sockets for TCP healthchecks.
  - **Action:** Remove `NET_RAW` to maintain the strict `cap_drop: [ALL]` baseline.
- [x] **Rename Redis `CONFIG` Command:** `SECURITY_AND_OPSEC.md` §6 requires dangerous commands to be disabled. The compose file renames `FLUSHALL` and `DEBUG` but misses `CONFIG`.
  - **Action:** Add `--rename-command CONFIG ""` to the Redis entrypoint.

## 3. Functionality & Architecture Drift

- [ ] **Add Tests for Phase 4 Catalog Code:** The repo contains ~90KB of untested catalog/vendor handlers (`catalog_handlers.go`, `product_handlers.go`, `vendor_handlers.go`, `catalog_store.go`) and migration `0007_catalog_phase4`, yet `README.md` and `TASKS.md` still mark Phase 4 as pending/unchecked.
  - **Action:** Update `TASKS.md` to reflect reality, and write unit/integration tests for the existing catalog logic.
- [x] **Document the Product Image Upload Pipeline:** `ARCHITECTURE.md` §8 claims "no file upload surface," yet `internal/image/scrub.go` exists and nginx plans for uploads.
  - **Action:** Document the pipeline: magic-byte validation, size caps, metadata stripping, re-encoding to a fixed raster format (no SVGs allowed), and storage paths.
  - **Closed:** documented in `SECURITY_AND_OPSEC.md` §4.3 (magic-byte checks, size caps, EXIF/metadata strip, raster re-encode, no SVG).
- [x] **Implement Postgres Full-Text Search (FTS):** `ILIKE` will not scale for a real catalog.
  - **Action:** Add `tsvector` columns with GIN indexes and `pg_trgm` for typo-tolerant search in the catalog migration.
  - **Closed for tsvector / open for trigram (verified 2026-11-27):** no `ILIKE` remains in any non-test Go source. `qProductsSearch` (`auth_handlers.go:257`) and `catalog_store.go` use `to_tsvector('simple', …) @@ plainto_tsquery(…)` with `ts_rank` ordering, backed by the GIN index `products_search_idx` (`db/migrations/0001_core_schema.up.sql:61`). Residual: `pg_trgm` typo tolerance is still absent from every file under `db/` — track it as a follow-up, not a blocker.
- [ ] **Add CI Smoke Test:** Add a compose-level integration test to CI that boots the stack, hits `/healthz` over the intended ingress path, and asserts the exact security headers are present. This would have caught the HSTS, port mapping, and CSP split-brain bugs.
  - **Action:** Add the compose-level integration test.
- [ ] **Implement Core Features:** Build Phases 5–7 (Cart, Checkout, Monero payment polling, Escrow state machine, Disputes, Tickets UI, Messaging UI) as outlined in `TASKS.md`.

## 4. No-JS Modern UI Implementation Guide

The "no JS" constraint is entirely compatible with a sleek, modern aesthetic using HTML5 and CSS3. Because Tor Browser is ESR 128+, modern CSS is fully supported.

### 4.1. Design Tokens & Constraints
Move all styling out of inline `style="..."` attributes (allowing you to tighten CSP to `style-src 'self'`) and into a single `main.css` file.

- **Colors & Spacing:** Use CSS variables (`oklch()` for modern color spaces) and `clamp()` for fluid typography/spacing.
- **Layout:** Use CSS Grid and `position: sticky` (for headers and filter sidebars).
- **Performance:** Serve exactly one uniform asset set across all pages (required by `SECURITY_AND_OPSEC.md` §1 to prevent fingerprinting). Keep the initial payload under ~200KB.
- **Typography:** Use `font-family: system-ui, sans-serif;` to avoid downloading web fonts, which is faster over onion circuits and reduces fingerprinting surface.
- **Images:** Use `aspect-ratio` + explicit `width`/`height` on product images to prevent Cumulative Layout Shift (CLS). Use `loading="lazy"` on images below the fold.

### 4.2. Pure HTML/CSS Interaction Patterns

#### Accordions (FAQ, Specs, Shipping)
Use native `<details>` and `<summary>`. It is accessible, requires zero JS, and is already mandated by `AGENTS.md` §3.

#### Tabs (Description / Reviews / Vendor)
Use radio inputs + labels + sibling selectors for bookmarkable, accessible tabs.

```html
<div class="tabs">
  <input type="radio" name="t" id="t1" checked>
  <label for="t1">Description</label>
  
  <input type="radio" name="t" id="t2">
  <label for="t2">Reviews ({{.ReviewCount}})</label>
  
  <section class="panel p1">…</section>
  <section class="panel p2">…</section>
</div>
```

```css
.tabs input { position: absolute; opacity: 0; }
.panel { display: none; }
#t1:checked ~ .p1, #t2:checked ~ .p2 { display: block; }

label { padding: .5rem 1rem; cursor: pointer; border-bottom: 2px solid transparent; }
input:checked + label { border-color: var(--accent); }
input:focus-visible + label { outline: 2px solid var(--accent); outline-offset: 2px; }
```

#### Modals / Lightboxes
Use the `:target` pseudo-class for overlays.
*Note: Without JS, you cannot trap focus inside the modal. Keep modals non-critical (e.g., "About this image") and prefer inline `<details>` for important flows.*

#### Autocomplete (Categories, Search Terms)
Use `<input list>` and `<datalist>`. It is natively supported and requires no JS.

#### Star-Rating Input
Use the reverse-ordered radio button technique. Hide the actual radio buttons with `opacity: 0` and style the `label`s using CSS gradients or `clip-path`.

#### Dark Mode Toggle
Use a `POST` form that sets a theme cookie, and have the server render `<html data-theme="dark">`. Alternatively, rely purely on `@media (prefers-color-scheme: dark)`.

#### Site Search from Address Bar
Ship an `opensearch.xml` and `<link rel="search">`. Tor Browser/Firefox will natively pick it up, giving users a free UX win with zero JS.

### 4.3. Bootstrap Options
Rather than hand-rolling from zero, consider vendoring a small classless CSS base locally (no CDNs allowed per `AGENTS.md` §1).
- **Pico.css:** (~10KB, MIT, semantic HTML, automatic dark mode, excellent forms).
- **Water.css:** Extremely lightweight, classless.

*Tor Constraints:* Avoid `backdrop-filter`-heavy "glassmorphism" as it can be resource-intensive or glitchy at the "Safest" security slider level. Test every pattern at all three Tor Browser security levels.

## 5. Recommended Action Sequence

1. ~~**Stabilize the Foundation:**~~ **Done** — all Section 1 config contradictions (HSTS, port mapping, container binding, CSP split-brain, socket/TCP reconciliation, backup egress, BTCPay stub) are closed; see the checkmarks above.
2. **Add CI Smoke Test:** Ensure `docker compose up` actually serves the site and enforces the unified headers.
3. **Resolve Auth/Authenticity:** Abandon the TLS/SPKI implementation, implement the two-step personal phrase login, and add TOTP 2FA.
4. ~~**Database Hardening:**~~ **Mostly done** — migration 0023 hardens the ledger (view fix + double-entry constraint trigger; literal REVOKE deferred because no least-privilege DB role exists yet — tracked in TASKS.md), Redis `CONFIG` is renamed (`docker-compose.yml`), Tor PoW is enabled (`deploy/torrc`). Remaining: bootstrap an `onionshop_app` role, then REVOKE binds.
5. **Feature & Test Parity:** Catalog documentation drift is resolved (README/TASKS reflect Phases 1–10 as implemented); image pipeline documented. Remaining: unit/integration tests for catalog handlers, text CAPTCHA alternative, register-path enumeration review, TOTP enrollment UX (QR/otpauth URI), and the CI smoke gate. Note: Cart/Checkout/Escrow already exist — the open work is production-gate evidence, not feature builds (see PRODUCTION_READINESS.md).


## Closed findings — BTCPay removal and Monero settlement (verified 2026-11-24)

| Finding | Evidence of closure |
|---|---|
| `btcpay` image digest `sha256:e63283733215...f04620f4c00000000` was a placeholder, not a real digest (pull would fail) | `btcpay` service absent from `docker-compose.yml` |
| No `bitcoind` and no NBXplorer anywhere in the stack; `BTCPAY_CHAINS`/`BTCPAY_BTCEXPLORERURL` unset, so payment detection was impossible | no Bitcoin daemon or explorer service in compose; settlement is Monero-only |
| `read_only: true` with only `/tmp` tmpfs left BTCPay nowhere to write keys/plugins/logs | service removed |
| `btcpay_net` was `driver: bridge` without `internal: true`, dual-homing the service — an egress hole contradicting ARCHITECTURE §2 | network removed; remaining networks reviewed for `internal: true` |
| `btcpaydb` reused `${DB_USER}`/`${DB_PASSWORD}` — one credential leak crossed both databases | service and `btcpay_pgdata` volume removed; no shared credentials |
| `BTCPAY_API_URL` / `BTCPAY_API_KEY` in app environment and installer secret generation | removed from compose and `install/install.sh` |
| Checkout hardcoded `InvoiceLink: "https://btcpay.example.com/invoice?id=test"` | replaced by real per-order subaddress creation via wallet-rpc |
| Float conversions in the money path (`price_usd * 100`, `amountXMR * 1e12`) | `parseDecimalUnits` exact integer parsing with unit tests |
| Withdrawal approval never moved value | marking `paid` broadcasts via `SendTransaction`, stores `tx_hash`, posts an idempotent ledger debit |
| Buyer delivery instructions discarded at checkout | encrypted to the vendor's public key and stored in `shipping_ciphertext`; server never decrypts |

Residual open items are tracked in `TASKS.md` (view-only ceremony, confirmation policy,
stagenet rehearsal, host assertions) and `PRODUCTION_READINESS.md`.

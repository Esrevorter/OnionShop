# Audit and Action Plan

This document serves as an independent review of the repository's current state, cross-referencing the architectural intent documented in `ARCHITECTURE.md`, `SECURITY_AND_OPSEC.md`, and `AGENTS.md` against the actual implementation in `nginx/nginx.conf`, `docker-compose.yml`, `deploy/torrc`, and the Go codebase. It also provides a concrete implementation guide for achieving a modern, no-JS UI.

---

## 1. Critical Configuration Contradictions (Fix First)

These items represent conflicts where the documentation explicitly forbids a configuration, yet the shipped files implement it (or vice versa), resulting in broken deployments or violated constraints.

- [ ] **Remove HSTS from Nginx:** `nginx.conf` currently emits `Strict-Transport-Security`. This violates the explicit "no browser-facing HSTS" rule in `AGENTS.md` §4, `CONTRIBUTING.md` #4, `ARCHITECTURE.md` §3, `SECURITY_AND_OPSEC.md` §1, and `internal/server/headers.go`. With a self-signed cert, HSTS creates a permanent, non-bypassable lockout if the cert is ever regenerated (breaking the SPKI pin).
  - **Action:** Delete the `add_header Strict-Transport-Security ...` line from `nginx.conf`.
- [ ] **Fix Nginx/Compose Port Binding & Reachability:** As shipped, `deploy/torrc` maps `HiddenServicePort 80 → 127.0.0.1:8443` (TLS), but `nginx.conf` has no plain-HTTP listener (meaning `http://` fails), and port `443` is unmapped (meaning `https://` fails). Furthermore, `nginx.conf` binds `listen 127.0.0.1:8443` *inside the container*, but compose publishes the host port to the container's bridge interface, not loopback.
  - **Action:** Implement the redirect listener in `nginx.conf` (`listen 8080` returning 301 to `https://`), add `HiddenServicePort 443 → 127.0.0.1:8443` to `deploy/torrc`, and change `listen 127.0.0.1:8443 ssl;` to `listen 8443 ssl;` in `nginx.conf` so the compose publisher can reach it.
- [ ] **Unify Content Security Policy (CSP):** `headers.go` (matching `ARCHITECTURE.md` §3) uses `script-src 'none'` and `style-src 'self' 'unsafe-inline'`. `nginx.conf` uses `script-src 'self'` and `style-src 'self'` (no `unsafe-inline`). Because nginx `add_header` *adds* headers rather than overwriting them, browsers receive both and enforce the intersection, blocking all inline styles and violating the absolute no-JS rule (`script-src 'self'` allows JS execution).
  - **Action:** Make the CSP string in `nginx.conf` byte-identical to the constant in `internal/server/headers.go`. Add a CI test that diffs the two strings.
- [ ] **Reconcile UNIX Socket vs. TCP:** Five documents (`README`, `ARCHITECTURE` §1, `SECURITY` §5.1, `INSTALL` §6, `TASKS` Phase 1) mandate a UNIX socket (`app.sock`) with no TCP hop. The actual `docker-compose.yml` and `nginx.conf` use a TCP bridge (`upstream { server app:8080; }`).
  - **Action:** Either implement the shared `appsock` volume in compose and update `nginx.conf` to `proxy_pass http://unix:/run/onionshop/app.sock;`, OR update all five markdown files to reflect the internal TCP bridge reality.
- [ ] **Fix Backup Sidecar Egress:** The `backup` container in `docker-compose.yml` has an entrypoint that runs `apk add postgresql16-client age` at boot. However, the service is on `internal_net` (`internal: true`), meaning it has no internet access and will fail to start.
  - **Action:** Remove the runtime `apk add`. Bake `pg_dump` and `age` into the backup image via a multi-stage `Dockerfile.backup`.
- [ ] **Resolve BTCPay Stub & Egress Rules:** The BTCPay container violates the "no floating tags" rule (`latest`), lacks NBXplorer/bitcoind for on-chain tracking, and needs blockchain egress, which conflicts with the host's "egress only for the tor uid" nftables policy.
  - **Action:** Pin the image to a digest, add the NBXplorer/bitcoind compose stack, and document the Tor-routed sync path for the host firewall.

---

## 2. Security & OpSec Gaps

Controls that are missing from the current implementation or threat model, which are necessary before handling real escrow funds.

- [ ] **Implement 2FA (TOTP):** Add server-rendered QR code generation at enrollment and a 6-digit TOTP field for Vendor, Mod, and Admin accounts. Ensure printable recovery codes are generated.
- [ ] **Define Account Recovery SOP:** Support-desk social engineering is the #1 account takeover vector. Define a strict PGP-signed recovery procedure (or an explicit "no recovery" policy) and ensure every step is audit-logged.
- [ ] **Implement Two-Step "Personal Phrase" Login:** `SECURITY_AND_OPSEC.md` §4 suggests users verify their phrase *before* logging in, but currently, it only renders on the `/account` page (post-auth).
  - **Action:** Implement a two-step login. `GET /login` takes a username; the server renders the user's personal phrase alongside the password field.
- [ ] **Publish Both Certificate Fingerprints:** Tor Browser's cert viewer shows the *certificate's* SHA-256 fingerprint, not the Subject Public Key Info (SPKI) hash. Ordinary users cannot verify the SPKI pin.
  - **Action:** Update `pgp.txt` and announcements to include **both** the Cert SHA-256 and the SPKI SHA-256, with a screenshot guide for Tor Browser users.
- [ ] **Enable Tor Proof-of-Work (PoW) DoS Defense:** Onion services are vulnerable to rendezvous circuit exhaustion.
  - **Action:** Add `HiddenServicePoWDefensesEnabled 1`, `HiddenServicePoWQueueRate 25`, and `HiddenServicePoWQueueBurst 50` to `deploy/torrc` (requires Tor >= 0.4.8).
- [ ] **Enforce Ledger Immutability at the DB Layer:** The ledger is described as "append-only," but the application user currently has `UPDATE`/`DELETE` privileges on ledger tables.
  - **Action:** `REVOKE UPDATE, DELETE ON ledger_lines FROM onionshop_app;`. Add a Postgres trigger to assert that debits equal credits for every transaction.
- [ ] **Improve CAPTCHA Accessibility & Honeypot Security:** Visual-only CAPTCHAs exclude users. The honeypot field name (`sta`) is public in the open-source repo.
  - **Action:** Add a text/logic alternative to the CAPTCHA. Randomize the honeypot field name per-deployment via `.env` at install time, and add `aria-hidden="true" tabindex="-1" autocomplete="off"`.
- [ ] **Prevent Username Enumeration on `/register`:** Ensure the registration endpoint returns a uniform success/error message (or uses email/PGP verification links) so attackers cannot harvest valid usernames via timing or error responses.
- [ ] **Remove Unnecessary Linux Capabilities:** The `app` container has `cap_add: [NET_RAW]`. Busybox `wget` does not require raw sockets for TCP healthchecks.
  - **Action:** Remove `NET_RAW` to maintain the strict `cap_drop: [ALL]` baseline.
- [ ] **Rename Redis `CONFIG` Command:** `SECURITY_AND_OPSEC.md` §6 requires dangerous commands to be disabled. The compose file renames `FLUSHALL` and `DEBUG` but misses `CONFIG`.
  - **Action:** Add `--rename-command CONFIG ""` to the Redis entrypoint.

---

## 3. Functionality & Architecture Drift

- [ ] **Add Tests for Phase 4 Catalog Code:** The repo contains ~90KB of untested catalog/vendor handlers (`catalog_handlers.go`, `product_handlers.go`, `vendor_handlers.go`, `catalog_store.go`) and migration `0007_catalog_phase4`, yet `README.md` and `TASKS.md` still mark Phase 4 as pending/unchecked.
  - **Action:** Update `TASKS.md` to reflect reality, and write unit/integration tests for the existing catalog logic.
- [ ] **Document the Product Image Upload Pipeline:** `ARCHITECTURE.md` §8 claims "no file upload surface," yet `internal/image/scrub.go` exists and nginx plans for uploads.
  - **Action:** Document the pipeline: magic-byte validation, size caps, metadata stripping, re-encoding to a fixed raster format (no SVGs allowed), and storage paths.
- [ ] **Implement Postgres Full-Text Search (FTS):** `ILIKE` will not scale for a real catalog.
  - **Action:** Add `tsvector` columns with GIN indexes and `pg_trgm` for typo-tolerant search in the catalog migration.
- [ ] **Add CI Smoke Test:** Add a compose-level integration test to CI that boots the stack, hits `/healthz` over the intended ingress path, and asserts the exact security headers are present. This would have caught the HSTS, port mapping, and CSP split-brain bugs.
- [ ] **Implement Core Features:** Build Phases 5–7 (Cart, Checkout, BTCPay polling, Escrow state machine, Disputes, Tickets UI, Messaging UI) as outlined in `TASKS.md`.

---

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
.tabs input { position: absolute; opacity: 0; }
.panel { display: none; }
#t1:checked ~ .p1, #t2:checked ~ .p2 { display: block; }

label { padding: .5rem 1rem; cursor: pointer; border-bottom: 2px solid transparent; }
input:checked + label { border-color: var(--accent); }
input:focus-visible + label { outline: 2px solid var(--accent); outline-offset: 2px; }.tabs input { position: absolute; opacity: 0; }
.panel { display: none; }
#t1:checked ~ .p1, #t2:checked ~ .p2 { display: block; }

label { padding: .5rem 1rem; cursor: pointer; border-bottom: 2px solid transparent; }
input:checked + label { border-color: var(--accent); }
input:focus-visible + label { outline: 2px solid var(--accent); outline-offset: 2px; }

# OnionShop Repository: Actionable Audit & Remediation List

Based on a thorough analysis of the **OnionShop** repository (including code, configuration files, documentation, and CI pipelines), here is a comprehensive, actionable list of issues that require fixing, patching, updating, or testing.

---

## 🚨 1. Critical Security & Configuration Issues

| Component | Issue | Action Required |
| :--- | :--- | :--- |
| **`docker-compose.yml`** | **Fake Docker Digest**: The `btcpay` service uses a placeholder SHA256 digest ending in `00000000`. This will cause `docker compose up` to fail when attempting to pull the image. | Replace with a valid, verified digest or a pinned minor tag (e.g., `btcpayserver/btcpayserver:1.13.0`) with a clear TODO to pin by digest once verified. |
| **`scripts/panic-wipe.sh`** | **Ineffective `shred` usage**: The script uses `shred` on `.env`, `.secrets`, and tmpfs paths. `shred` is notoriously unreliable on modern filesystems (ext4, btrfs, zfs) and completely ineffective on tmpfs (memory-backed). | Replace `shred` with `rm -f` for tmpfs paths. Add a prominent comment that file-level shredding is not cryptographically secure on modern filesystems; true security relies on destroying the LUKS keyslots (which the script correctly notes as a manual next step). |

---

## 🐛 2. Code & Script Bugs

| Component | Issue | Action Required |
| :--- | :--- | :--- |
| **`scripts/backup.sh`** | **Typo in variable fallback**: `INTERVAL="${INTERBACK_INTERVAL_SECONDS:-${INTERVAL_SECONDS:-86400}}"` contains a typo (`INTERBACK`). | Fix to `INTERVAL="${INTERVAL_SECONDS:-86400}"`. |
| **`scripts/backup.sh`** | **Sloppy decryption check**: The script runs `age -d -i /nonexistent "$ENC"`, which is guaranteed to fail. While the comment acknowledges this, it is poor practice. | Remove the fake decryption check. Rely on the documented "quarterly restore drills" for actual decryptability testing, or implement a proper metadata-only validation. |
| **`docker-compose.yml`** | **Healthcheck DNS reliance**: The `nginx` healthcheck uses `http://app:8080/healthz`. If strict DNS-leak prevention is enforced per `SECURITY_AND_OPSEC.md`, internal DNS resolution might be blocked or unreliable. | Change the `nginx` healthcheck to verify the UNIX socket directly (if `wget`/`curl` supports it in the Alpine image) or rely on the `app` service's own healthcheck to avoid internal DNS lookups. |

---

## 🛠️ 3. Build & Dependency Issues

| Component | Issue | Action Required |
| :--- | :--- | :--- |
| **`go.mod` & `ci.yml`** | **Misleading Go Version Requirements**: The files specify `go 1.25.0` / `1.25.12` and claim `pgx v5.9.x` requires it. This is factually incorrect (pgx v5 requires Go 1.20/1.21+). The comments about `golangci-lint` choking on "Go 1.25/1.27-era compilers" appear to be hallucinated or based on a misunderstanding. | Downgrade the `go` directive in `go.mod` to a realistic, stable version (e.g., `go 1.22` or `1.23`). Update the CI workflow accordingly and remove the misleading comments. |
| **`Makefile`** | **Missing `generate` dependency**: `TASKS.md` states `internal/store/gen/` is gitignored. However, the `build` and `test` targets do not depend on the `sqlc` target. A fresh clone will fail to build. | **Option A (Recommended)**: Commit the `sqlc` generated code to the repository for deterministic builds.<br>**Option B**: Add `sqlc` as a prerequisite to the `build` and `test` targets in the `Makefile` (e.g., `build: sqlc`). |

---

## 📝 4. Documentation Inconsistencies

| Component | Issue | Action Required |
| :--- | :--- | :--- |
| **`TASKS.md`** | **Outdated PGP Library Reference**: Claims the app uses `golang.org/x/crypto/openpgp`, which is officially deprecated and frozen. | Update `TASKS.md` to reflect that the code correctly uses the maintained fork: `github.com/ProtonMail/go-crypto/openpgp` (which is already implemented in `internal/server/pgp.go`). |
| **`INSTALL.md`** | **Missing Backup Prerequisite**: The `docker-compose.yml` references `Dockerfile.backup`, which requires an `AGE_PUBLIC_KEY`. | Ensure `INSTALL.md` explicitly documents the requirement to generate and set `AGE_PUBLIC_KEY` in the `.env` file before running `make up`. |

---

## 🚧 5. Pending Features (Requires Development)

Per `TASKS.md`, the following components are marked as incomplete (`[ ]`) and require active development before the project can be considered feature-complete:

- **Phase 3 (Auth)**: Finalize RBAC Middleware (account tiers are pending).
- **Phase 4 (Catalog)**: Vendor registration and Admin approval flow (with audit logging).
- **Phase 5 (Checkout)**: Wallets page (per-currency available balance, in-escrow total, deposit/withdraw UI).
- **Phase 6 (Escrow)**: Escrow state machine including failure paths (cancel/expiry, under/overpayment) and auto-finalization cron.
- **Phase 7 (Support)**: 
  - Support ticket CRUD (categories, statuses, order linking).
  - Staff queue UI (filter/claim/reply/resolve with mandatory audit logging).
  - Ticket ↔ dispute escalation.
  - Private messaging (thread list, conversation view, unread-count badge).
  - Image CAPTCHA service for every message POST.
  - Message abuse controls (inert-text URLs, no attachments, "report message").
- **Infrastructure Hardening**: Finalize host-level `nftables` default-deny templates, systemd sandbox configurations, Dropbear admin onion setup, and warrant canary automation.

---

## 🔬 6. CI/CD & Testing Improvements

| Component | Issue | Action Required |
| :--- | :--- | :--- |
| **`.github/workflows/ci.yml`** | **Permissive Vulnerability Scanning**: The `vuln` job using `govulncheck` is set to `continue-on-error: true`. | Once the Go version and dependencies are stabilized, remove `continue-on-error: true` to enforce strict vulnerability checks and prevent merging vulnerable dependencies. |
| **`docker-compose.yml`** | **Tag Pinning vs. Digest Pinning**: `TASKS.md` correctly identifies that Docker images should be pinned by digest, not minor tags (e.g., `nginx:1.27-alpine`). | Update `docker-compose.yml` to use SHA256 digests for `nginx`, `postgres`, `redis`, and `alpine` base images to prevent supply chain mutations. |

---

## 💡 Recommended Immediate Next Steps

1. **Fix the fake BTCPay digest** in `docker-compose.yml` to unblock local testing.
2. **Resolve the SPKI pinning contradiction** by aligning all documentation and scripts with the `SECURITY_AND_OPSEC.md` standard (onion self-authentication).
3. **Correct the Go version** in `go.mod` and `ci.yml` to a real, supported version and clean up the misleading comments.
4. **Fix the `Makefile`** to ensure `sqlc generate` runs before `build` or `test`, or commit the generated code.

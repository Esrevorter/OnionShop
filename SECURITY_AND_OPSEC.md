# Security & Operational Security (OpSec) Guidelines

> **Threat model.** Assume every packet between the user and our Tor daemon transits
> adversary-controlled relays; assume the host may be seized cold at any moment; assume the
> clearnet IP of the operator is a high-value target. Every control below exists to make one of
> those assumptions harmless. Layers are **defense-in-depth**: onion services already provide
> transport encryption and endpoint anonymity, so inner layers (TLS, SSH crypto, disk
> encryption) are hardening, not the primary line of defense.

## 1. Tor Hidden Service Specifics
- **Localhost Binding:** The Go web server MUST bind ONLY to a UNIX socket (`/run/onionshop/app.sock`)
  or `127.0.0.1:PORT`. Never bind to `0.0.0.0`. nginx (the reverse proxy, §5.1) is the only listener
  the Tor daemon talks to, also loopback-only.
  The only process allowed to accept from outside localhost is the Tor daemon itself, and even it
  listens only on loopback interfaces reachable through its own socket.
- **Tor Daemon Hardening (`torrc`):**
  - Publish exactly ONE onion service per host role; `HiddenServiceDir` isolated per service with
    `0700` permissions owned by the `tor` user. The private key (`hs_ed25519_secret_key`) lives on
    an encrypted volume and is backed up encrypted (see §8).
  - `SocksPort 0` (no open SOCKS proxy); if local tooling needs socks, bind it to
    `127.0.0.1:9050` with `SocksPolicy reject *`.
  - `HiddenServiceStatistics 0`; do not enable traffic-shaping telemetry that could be subpoenaed.
  - Run the relay/service as the dedicated unprivileged `tor` user inside its own
    systemd sandbox (`PrivateTmp`, `ProtectSystem=strict`, `IPAddressAllow=127.0.0.1`,
    `IPAddressDeny=any` except established outbound).
  - Keep Tor updated via a pinned, verified repository (deb.torproject.org key pinned out-of-band);
    subscribe to tor-announce for exit-list and vulnerability advisories.
- **No IP Rate Limiting:** Traditional IP-based rate limiting (like Fail2Ban) is useless here. Use Redis-based sliding window rate limiting keyed by `session_id` or `user_id`.
  - **Caveat:** anonymous attackers can mint fresh sessions to reset session-keyed limits. Wherever possible, key limits to the authenticated `user_id`; for pre-auth endpoints (login, registration, CAPTCHA), layer cheap friction first (image CAPTCHA + honeypot + global throughput cap — see AGENTS.md §3) so session-minting stays uneconomical.
- **Time Synchronization:** Do not implement custom NTP logic. Rely on the host OS. Do not leak timezones in HTTP headers. All logs, tokens, and cron decisions use UTC end-to-end.
- **Header Hygiene:** Strip/genericize the `Server:` header, never expose stack traces in error pages (render a generic template), and never register `pprof` or `expvar` routes. Serve uniform static assets (one CSS file, one font set, one image style) so per-page fingerprinting yields nothing.
- **HTTPS / Onion TLS (defense-in-depth):** Terminate TLS at nginx using a **self-signed certificate
  for the `.onion` hostname** (Let's Encrypt cannot validate .onion names via HTTP-01/DNS-01 in a
  way that adds real trust; a CA-signed cert would falsely imply third-party attestation).
  - Generate the key on the encrypted host, permission `0600`, owned by `root`, mode-readable only
    by the nginx user via group.
  - Pin the certificate **fingerprint (SPKI SHA-256)** in the PGP-signed announcement post and in
    `pgp.txt`/help-center documentation so users can verify what they should expect in Tor Browser's
    security info. Document clearly that this is authenticity-of-config, not identity-of-operator.
  - If a future release ships a client (CLI/desktop) that consumes the API, enforce SPKI pinning in
    that client and enable HSTS *there* — but keep browser-facing behavior conservative because
    browsers treat self-signed + HSTS as a permanent lockout footgun.
  - TLS config: minimum TLS 1.3 preferred, TLS 1.2 fallback with AEAD suites only
    (`TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384`, `TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305`);
    disable compression, renegotiation, and CBC padding paths; no OCSP stapling (leaks to CAs).

- **Tor Daemon Hardening (`torrc`) — operational recap:** one public onion service per host role, `HiddenServiceDir` at `0700` owned by `tor`, `SocksPort 0` (or loopback-bound with `SocksPolicy reject *`), `HiddenServiceStatistics 0`, unprivileged systemd sandbox for the daemon, and updates only from the key-pinned torproject repository. The onion's `hs_ed25519_secret_key` lives on the encrypted volume (§5.5) and is backed up encrypted; rotation happens only with a PGP-signed migration announcement (§8).

## 2. Data Minimization & Privacy
- **No Analytics:** Do not implement Google Analytics, Plausible, or any tracking scripts.
- **Log Sanitization:** Ensure the Go logger strips out any potential PII. Log only errors and critical system events.
- **Log Storage & Retention:** Application logs live on a size-capped tmpfs-backed buffer
  (volatile journald where possible) and rotate aggressively; anything that must persist goes only
  to the encrypted volume after PII scrubbing. No log shipping off-host, ever.
- **PGP Handling:** The server stores only user **public** keys and ciphertext. Private keys never touch the server; decryption of shipping addresses happens client-side on the vendor's machine. Process public-key import in memory, validate armored-block format, and never log raw key material or ciphertext.

## 3. Payment & Escrow Security
- **Payment Status via Polling (no webhooks):** A localhost-only hidden service cannot receive inbound webhooks from BTCPay (see ARCHITECTURE.md §2). The backend polls BTCPay's invoice API; every poll result is applied idempotently (unique `(invoice_id, status)` event key with a `UNIQUE` constraint) so duplicate or out-of-order updates can never double-credit escrow. Validate that the polled invoice's amount, currency, and `outOrderRef` match the local order before transitioning state. If signed webhooks are ever added as an optimization, the handler MUST cryptographically verify the `BTCPay-Sig` HMAC header before processing anything.
- **Ledger Integrity:** All ledger updates MUST be executed within a PostgreSQL transaction (`BEGIN`, `COMMIT`, `ROLLBACK`) to prevent race conditions. Ledger tables are append-only double-entry (see ARCHITECTURE.md §6); balances are never mutated in place.
- **Audit Trail:** Every moderator/admin action (dispute resolution, forced release/refund, vendor approval, withdrawal approval) writes an immutable audit-log row (actor, action, target, timestamp UTC, reason). Automated cron transitions are logged the same way with actor = `system`.
- **No Direct Payouts:** Never send BTC directly from an order checkout. All funds go to the platform escrow wallet; vendors withdraw via requests approved by an admin.

## 4. Anti-Phishing Measures (support & messaging)
Cloned .onion sites steal credentials by replaying our exact HTML; these controls help users
verify authenticity and slow automated phishing campaigns. See ARCHITECTURE.md §7–8.
- **Per-message anti-phishing CAPTCHA:** every private message and ticket reply requires a
  fresh, single-use visual challenge generated locally (no external services, no JS). The
  correct answer is derived from per-user state so a clone site cannot know it out-of-band.
- **Personal phrase:** each user sets a secret phrase rendered on their account pages and in
  support-ticket confirmations; if a "market" doesn't show it, the user knows the site is a
  clone. Prompt users to verify it before logging in or depositing.
- **Login history with mirror/host column:** record UTC timestamp + the `Host` header the
  login arrived on (never client IPs). Users see "which address did I just log in through?" —
  an unknown host string is a phishing red flag.
- **Inert URLs in messages:** user-posted links are displayed as plain text, never clickable,
  so clones can't harvest clicks and scam lures can't masquerade as market links.
- **Official-domain discipline:** publish the canonical .onion plus PGP-signed announcements
  (news board), a `canary.txt`, and a static `/pgp.txt`; warn against search-engine mirrors.
  Discourage third-party reverse proxies entirely (they are credential-harvesting machines).
- **Abuse reporting loop:** one-click "report message" → support ticket (category: vendor
  report/phishing); staff can suspend accounts; suspensions are audit-logged with reason.
- **Optional DDoS friction (documented, not default):** proof-of-work gates or image CAPTCHA
  on login during attacks. Keep any PoW logic tiny and auditable; never require JS.

## 5. Infrastructure & Network Hardening

### 5.1 Reverse Proxy — nginx in front of the Go app
Every request from the Tor daemon hits **nginx first**, never the Go binary directly. This gives
us TLS termination (§1), header stripping, static-file serving, buffering against slow clients,
and an isolation boundary so a Go bug is not also an exposed listener.
- Listen on `127.0.0.1:8080` (HTTP → 301 redirect) and `127.0.0.1:8443` (TLS); both loopback.
  Tor maps onion ports onto these via `HiddenServicePort 80 127.0.0.1:8080` and
  `HiddenServicePort 443 127.0.0.1:8443`.
- `server_tokens off;` and override any residual `Server:` value; internal headers
  (`X-RateLimit-*`, etc.) are stripped before responses leave nginx.
- Serve `/static/*` directly from a read-only mount with long-lived immutable cache hashes;
  proxy everything else to the Go upstream over a **UNIX socket**
  (`proxy_pass http://unix:/run/onionshop/app.sock;`) — no TCP hop between nginx and app.
- Buffers/timeouts: `proxy_buffering on`, hard `client_max_body_size 16k` (forms only — there
  are no uploads), `client_body_timeout 10s`, `client_header_timeout 10s`, small `limit_req`
  zones on POST endpoints to blunt slowloris and form-spam from malicious exits.
- Strip inbound noise before it reaches Go: drop `X-Forwarded-For`, `X-Real-IP`, and any
  client-supplied forwarding headers (`proxy_set_header X-Forwarded-* "";`) — we never want even
  a localhost-layer IP artifact in application logs.
- Emit the security headers from ARCHITECTURE.md §3 at the nginx layer AND re-assert them in Go
  middleware (belt-and-suspenders; both layers must agree).
- Run nginx as its own unprivileged user with a `read_only` rootfs + `tmpfs` on
  `/var/cache/nginx`, `/var/run`, `/tmp` (see docker-compose.yml). No shell binaries in the image.

### 5.2 Strict Firewall Rules (Default DROP)
Configure `nftables` (preferred) or `iptables` with default-deny on INPUT, FORWARD **and OUTPUT**.
Only allow:
  - Inbound: loopback only. Nothing routable binds a public interface — remote admin rides the
    onion network (§5.4), never a clearnet port.
  - Outbound: process-scoped allows — the `tor` user (owner-match) may open circuits; `btcpay`
    reaches peers only through Tor or dedicated wires; **every other egress is denied**, including
    the Go app and Postgres containers (compose `internal: true` networks enforce this too).
  - **Block all DNS resolution** to prevent DNS leaks: no resolver runs on the host; external APIs
    are reached by hardcoded IPs or through Tor's own DNS (`FakeResolv`/`AutomapHostsOnResolve`
    where unavoidable). Periodic canary checks assert zero UDP/TCP/53 egress.
  - Log dropped packets to a RAM-backed buffer only; ship nothing off-host.

### 5.3 OS & Service Isolation
  - Run the OS with a minimal footprint (Debian Minimal or Alpine; consider a Whonix-Gateway /
    AppVM split so the app host has no direct clearnet route at all).
  - Run each service as a separate, unprivileged user *and* group: `tor`, `nginx`, `postgres`,
    `redis`, `btcpay`, `onionshop`. No shared UIDs; no supplementary groups.
  - The Go binary and nginx must **never** run as `root`. Containers drop `CAP_ALL`
    (`cap_drop: [ALL]`) and add nothing back — we bind high loopback ports, so even
    `NET_BIND_SERVICE` is unnecessary.
  - Per-service sandboxing (systemd units): `ProtectSystem=strict`, `PrivateTmp=yes`,
    `NoNewPrivileges=yes`, `RestrictAddressFamilies=AF_UNIX AF_INET`,
    `ProtectKernelTunables`/`ProtectKernelModules`, `SystemCallFilter=@system-service` with
    deny-list (`@debug`, `@mount`, `@reboot`), seccomp/AppArmor profiles per unit.
  - **tmpfs for volatile state:** `/tmp`, `/var/tmp`, CAPTCHA PNG generation, PGP import buffers,
    QR rendering, invoice-poll caches, and decrypted secrets all live on `tmpfs` mounted
    `noexec,nosuid,nodev,size=<bounded>`. Sensitive intermediates are wiped on close and never hit
    disk. Exception rule: real database data and WAL stay on the encrypted volume — do NOT put
    Postgres on tmpfs (crash = loss); only ephemeral/rebuildable state belongs there. Redis
    persistence is OFF (`save ""`), so its whole dataset is legitimately RAM-only.
  - Swap disabled or fully encrypted (`cryptswap`); assert `swapon --show` is empty or LUKS-backed.
    Set `vm.swappiness=0` and `mlock` memory pools holding secrets (session store, signing keys)
    so they cannot page out.
  - Kernel hardening: `kernel.kptr_restrict=2`, `kernel.dmesg_restrict=1`,
    `kernel.yama.ptrace_scope=2`, `net.ipv4.tcp_syncookies=1`, disable ICMP redirects,
    `kernel.lockdown=integrity`.

### 5.4 Remote Administration — Dropbear SSH over a private onion service
Do **not** expose OpenSSH on a clearnet interface, and do not firewall SSH to a "trusted clearnet
IP" (that pins the operator's location and is correlation-friendly over time). Instead:
  - Publish a **second, non-public onion service** (v3, ed25519) whose
    `HiddenServicePort 22 127.0.0.1:2222` forwards to **Dropbear**, which listens only on
    `127.0.0.1:2222`. The admin connects through their own Tor SOCKS port
    (`ssh -o ProxyCommand='nc -x 127.0.0.1:9050 %h %p' ops@<admin-onion>`).
  - Why Dropbear: tiny, auditable attack surface (~10x smaller than OpenSSH), single binary suited
    to a read-only rootfs, modern curve25519/Ed25519 primitives, simple `noroot`/chroot restrictions.
  - Crypto policy (compile-time + runtime): KEX `curve25519-sha256` only; host key `ssh-ed25519`;
    ciphers `chacha20-poly1305@openssh.com` preferred with `aes256-gcm` fallback; AEAD only — no
    HMAC-SHA1/SHA2-etM MACs, no DH-group14/SHA1 legacy negotiation; **compression disabled**
    (info-leak + CPU-DoS risk).
  - Authentication: **key-only** Ed25519. Build Dropbear without password-auth support
    (`DROPBEAR_SVR_PASSWORD_AUTH` off) so there is no code path to misconfigure; no root login
    (`RESTRICT_ROOT`); admins log in as `ops` and `sudo` narrowly-scoped commands.
  - Trust pinning: the admin workstation records the admin-onion address **and** the SSH host-key
    fingerprint from a witnessed setup ceremony; any mismatch is treated as MITM and trips §8.
  - Intrusion hygiene: disable TCP/agent/X11 forwarding; nftables connection-rate limits on the
    loopback port; idle sessions force-closed after 15 minutes; every auth attempt appended to a
    RAM-only log flushed hourly to the encrypted volume; repeated failures lock the authorized-key
    entry pending out-of-band (PGP) re-enablement.
  - Fallback: PGP-encrypted "break-glass" sheet held offline describing local console access.
    Never a clearnet backdoor port, never a vendor "remote management" channel.
  - Keep OpenSSH uninstalled entirely unless a specific tool demands it.

### 5.5 Encryption at Rest & Key Management
  - Full-disk encryption with **LUKS2 (argon2id KDF, AES-XTS 512 or ChaCha20)** for root and every
    data volume: Postgres, backups staging, Tor hidden-service dirs, and admin key material.
    TPM-bound unlocking only with a PIN (measured boot alone is authentication theater).
  - Separate keyslots per purpose (daily unlock, backup mount, admin recovery) so revoking one
    does not require reprovisioning everything.
  - Plausibly-deniable hidden volume considered for the operator workstation; document the
    decision either way.
  - Application secrets (DB creds, Redis pass, session-signing key, BTCPay API tokens) come from an
    `age`/`systemd-creds`-sealed file opened at boot into a tmpfs `EnvironmentFile=` with mode
    `0400`; never baked into images, never in Git, never in Docker layer metadata.
  - Rotation schedule: session-signing key ≥ monthly; API tokens ≤ 90 days; onion identity rotated
    only with a PGP-signed migration announcement (careless rotation destroys the trust anchor).

### 5.6 Integrity & Supply Chain
  - Read-only rootfs (dm-verity or container `read_only: true`) everywhere it can be; writable
    surfaces are named volumes on LUKS or tmpfs.
  - Container images pinned by **digest**, not mutable tags; digests recorded in
    `docker-compose.yml` and reviewed like code changes; optional cosign/sigstore verification.
  - Reproducible Go builds (`CGO_ENABLED=0`, pinned toolchain in `go.mod`); release artifacts
    PGP-signed alongside the canary.
  - File-integrity monitoring (IMA/EVMS or minimum `apk audit`/`debsums` cron) reporting to local
    console only.
  - Time source: chrony inside the Tor circuit or the virtualization layer's clock — never direct
    clearnet NTP from the app host (traffic-analysis correlation risk).

## 6. Application & Secrets Management

- **Zero Hardcoded Secrets:** Database credentials, BTCPay API keys, and session secrets must **never** be committed to Git. Use environment variables or a secure secrets manager. The `.env` file must be in `.gitignore`.
- **Dependency Hygiene:** Regularly run `govulncheck ./...` to scan for known vulnerabilities in Go dependencies. Pin all dependency versions in `go.mod`; do not use floating `latest` tags.
- **Strict Content Security Policy (CSP):** The server must emit a strict CSP header on every response: 
  `Content-Security-Policy: default-src 'self'; script-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; upgrade-insecure-requests`
  (`script-src 'none'` and `base-uri 'none'` are mandatory — there is no JS anywhere;
  `upgrade-insecure-requests` pairs with the HTTPS rollout in §1.)
- **Database Least Privilege:** The Go application must connect to PostgreSQL using a dedicated database user that only has `SELECT`, `INSERT`, `UPDATE`, and `DELETE` permissions on specific tables. It must **not** have `SUPERUSER`, `DROP`, or `TRUNCATE` privileges. Migrations run under a separate short-lived role.
- **Redis Hardening:** `requirepass` + `maxmemory-policy noeviction` for the session DB, ACL users
  with least privilege (no `allcommands` user in production), dangerous commands renamed/disabled
  (`FLUSHALL`, `CONFIG`, `DEBUG`), bound to the compose-internal network only, persistence OFF
  (`save ""` + `appendonly no`) because sessions and rate counters are intentionally volatile.

## 7. Financial OpSec & Escrow Hardening

- **Withdrawal Delays:** Enforce a mandatory, non-bypassable delay (e.g., 24–48 hours) on all vendor withdrawal requests. This provides a critical window for admins to freeze the account and halt the payout if a vendor account is compromised or flagged for fraudulent activity.
- **Address Whitelisting:** Vendors must pre-register and PGP-verify their withdrawal BTC addresses. Withdrawals can only be sent to these pre-approved addresses. Any change to a withdrawal address requires a 7-day security hold and admin approval.
- **Hot/Cold Wallet Separation:** The BTCPay Server instance should only hold a minimal "hot wallet" balance required for daily operational refunds. The majority of the platform's escrowed funds should be periodically swept to a secure, multi-signature cold storage wallet not connected to the server.
- **Coin Control & Chain-Analysis Hygiene:** Never reuse addresses across orders; avoid auto-forward patterns that cluster the market's coins; sweep to the multisig cold wallet through a coordinator-free flow (no clearnet wallet-service APIs that would tag market funds).

## 8. Incident Response & Resilience

- **Warrant Canary:** Maintain a frequently updated, PGP-signed `canary.txt` file in the repository and on the site. If the server is compromised or served with a subpoena, the canary will not be updated, silently warning users without the operator being legally compelled to lie.
- **Panic Wipe Script:** Maintain a secure, easily executable script (triggered only by a specific, obscure file creation on the encrypted console volume or an action taken over the Dropbear-over-onion admin channel — NEVER reachable from the public web app) that immediately:
  1. Kills nginx, the Go application, and the database.
  2. Overwrites sensitive configuration files and encryption keys with random data (tmpfs contents
     vanish on unmount/power-loss by design — that is why volatile secrets live there).
  3. Unmounts and cryptographically shreds the LUKS volume (destroys all keyslots) so seizure yields ciphertext only.
- **Backup Encryption:** All database backups must be encrypted (e.g., using `age` or `gpg`) before being stored off-site. The decryption keys must never reside on the same server as the backups. Staging dumps happen in the backup container's tmpfs `/tmp` and are shredded after encryption succeeds.
- **Off-site Restore Drills:** Quarterly, restore an encrypted backup onto an isolated VM and verify ledger invariants (`SUM(debits) = SUM(credits)`) and audit-log continuity.
- **Post-incident protocol:** rotate the onion TLS key, session-signing key, and API tokens; re-publish the canary and a PGP-signed incident note; review which detection failed.

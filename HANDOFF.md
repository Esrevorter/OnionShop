# DEVLOG — 2026-11-16 — Dev stack bring-up: 14 defects found and fixed

## Scope

Attempting to stand up the documented dev stack (Docker Compose path) on a fresh
Debian 12 headless VM. The stack had never been booted end-to-end. Every defect
below was fatal-on-boot or functionally disabling, and none were detectable by
`go test ./...`, which was the only gate in CI.

**Environment:** Debian 12.14, kernel 6.1, LVM root on LUKS2, Docker Engine +
Compose v2, Go 1.25.12 (tarball, `GOTOOLCHAIN=local`), user `oniondev`.

**Outcome:** stack boots clean. Verified chain: host → published port → nginx
TLS 1.3 → UNIX socket → Go app → Redis → 200 `ok`.

---

## 1. Defect register

### A. Fatal on boot

| # | Defect | Cause | Fix | Status |
|---|---|---|---|---|
| 1 | BTCPay service cannot start | Image digest is a placeholder (`…f04620f4c00000000`); no bitcoind/NBXplorer defined | `profiles: ["disabled"]` on `btcpay` and `btcpaydb`; Phase 9 removes both | DONE |
| 2 | App permanently unhealthy, so nginx never created | `App.Serve` binds UNIX **or** TCP, never both. `ONIONSHOP_UNIX` set → TCP 8080 never bound, but healthcheck probed TCP | New `onionshop healthcheck` subcommand probes the same transport `Serve` bound | DONE |
| 3 | App cannot create its socket | `appsock` named volume seeded as `root:root 0755`; app runs uid 1000 | `Dockerfile`: `mkdir -p /run/onionshop && chown 1000:1000 && chmod 0770` in the runtime stage | DONE |
| 4 | nginx cannot reach the socket | `umask 0007` → socket is `srwxrwx--- 1000:1000`; nginx runs `101:101`. Compose never replicated the systemd path's shared `nginxsock` group | `group_add: ["1000"]` on nginx | DONE |
| 5 | nginx published ports silently ignored | A container attached only to `internal: true` networks has no default gateway; Docker publishes through the gateway network, so `ports:` is accepted and then never mapped | Added `edge_net` (non-internal, nginx-only) | DONE |
| 6 | App exits: `no page templates under web/templates` | `COPY web/` existed only in the build stage; the runtime image shipped no templates | `WORKDIR /app` + `COPY --from=build /src/web/templates ./web/templates` | DONE |
| 7 | postgres refuses to init | `user: "999:999"` — that's the **Debian** image convention; `postgres:16-alpine` uses uid/gid **70** | `user: "70:70"` | DONE |
| 8 | `initdb` cannot chmod its data dir | Volume root is root-owned and `cap_drop: [ALL]` removes `CAP_FOWNER` | `PGDATA: /var/lib/postgresql/data/pgdata` — initdb creates and owns a subdirectory | DONE |
| 9 | nginx `mkdir /var/cache/nginx/client_temp` → EACCES | Docker mounts tmpfs as `root:root`; nginx runs as 101 | `tmpfs: [/tmp:mode=1777, /var/cache/nginx:mode=1777]` | DONE |
| 10 | nginx `[emerg] SSL_CTX_set_cipher_list(...) failed (no cipher match)` | `ssl_ciphers` feeds the TLS≤1.2 API. TLS 1.3 suite names belong to `SSL_CTX_set_ciphersuites()` | Replaced with `ssl_conf_command Ciphersuites TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA384;` | DONE |
| 11 | nginx gets ENOENT on a socket that exists | Alpine's `/var/run` is a symlink to `/run`. Docker resolves it, so `tmpfs: /var/run` mounted over **`/run`**, shadowing the `appsock` volume at `/run/onionshop` | Dropped `/var/run` from tmpfs; `pid /tmp/nginx.pid;` | DONE |

### B. Resilience and observability

| # | Defect | Impact | Status |
|---|---|---|---|
| 12 | App degrades **permanently** on transient DB unavailability. A Docker *daemon* restart ignores `depends_on` ordering, so the app can start before postgres. `RegisterAuthRoutesFull` logs "auth routes not registered" and returns `nil` — the entire authenticated surface is unmounted, with no retry and no reconnect | After any host reboot the market can appear up while serving only anonymous 404 redirects. Only recoverable by manual restart | **OPEN** — fail fast (`log.Fatal`) or bounded retry |
| 13 | `/healthz` is not a readiness probe. It pings Redis only | It reported healthy while auth, catalog, vendor and admin routes were all unmounted | **OPEN** — assert pool is non-nil and routes are mounted |
| 14 | Misleading startup log: always prints `listening on <TCP addr> (TCP; bind policy enforced by resolveAddr)`, including when the UNIX socket is what was bound | Actively misdirects debugging | **OPEN** — log the actual listener |
| 15 | `net.Listen("unix", …)` does not unlink a stale socket inode. A SIGKILL, daemon restart or host reboot leaves the file behind and bind fails with `EADDRINUSE` | Crash-loop after any unclean shutdown | **OPEN** — `os.Remove` guarded by `ModeSocket` before bind |

### C. Security headers and TLS config

| # | Defect | Impact | Status |
|---|---|---|---|
| 16 | Security headers emitted twice — Go middleware and nginx `add_header` (which appends, not replaces) | Duplicate `X-Frame-Options` has undefined behaviour; some user agents ignore the header entirely, silently removing clickjacking protection. Identical CSP duplicates are harmless (they intersect) | **OPEN** — add `proxy_hide_header` for CSP/XFO/XCTO/Referrer-Policy/Permissions-Policy in the 8443 server block |
| 17 | `/static/` location defines `add_header Cache-Control`, so per nginx's inheritance rules it does **not** inherit the server-level security headers | Static responses likely served with no CSP, XFO or X-Content-Type-Options | **OPEN** — verify, then repeat the header set or `include` a shared fragment |
| 18 | `server_tokens off` hides the version, not the header; `proxy_hide_header Server` strips only the **upstream's** header | Responses still carry `Server: nginx`. §5.1's claim of stripping `Server` is not achievable with stock nginx | **OPEN** — correct the doc, or accept `Server: nginx` as the floor |
| 19 | `ssl_prefer_server_ciphers` is a TLS 1.2 knob, inert with `ssl_protocols TLSv1.3` | Harmless, but implies configuration that does nothing | DONE (removed) |

### D. Tooling and developer experience

| # | Defect | Impact | Status |
|---|---|---|---|
| 20 | `staticcheck@latest` requires Go ≥ 1.26; the pinned toolchain is 1.25.12 with `GOTOOLCHAIN=local` | Install fails outright | **OPEN** — pin `honnef.co/go/tools/cmd/staticcheck@v0.6.1`, which is exactly what golangci-lint v1.64.8 bundles |
| 21 | `make lint` invokes bare `staticcheck` and `golangci-lint` from `$PATH` with no version pins | Drifts from CI; `golangci-lint` isn't installed as a binary at all | **OPEN** — use the same `go run …@v1.64.8` form CI uses |
| 22 | Makefile's `DATABASE_URL ?=` default uses password `onionshop_dev_only`, while `install.sh` generates a random one | `make migrate` silently authenticates with a credential that cannot work | **OPEN** — require `DATABASE_URL` from the environment and fail loudly when unset |
| 23 | `make sqlc` is an undocumented prerequisite for `docker compose build app` | `internal/store/gen/` is gitignored but `COPY internal/` needs it; a clean clone fails to build with undefined symbols | **OPEN** — document, or run sqlc in the build stage |
| 24 | Test suite coupled to UI copy: `extractText(t, body, "CAPTCHA:")` | The responsive-UI redesign renamed the label to "Human verification" and moved the prompt into its own `<p>`. Five full-chain tests broke while the CAPTCHA itself remained fully functional | DONE — `postForm` now reads the prompt from `CaptchaService.Challenge()`; `extractText` removed; `TestFrictionContractOnRenderedForms` added asserting field **names**, not prose |
| 25 | `scripts/backup.sh` lacks the exec bit | Container exits 126 and restart-loops | DONE (`chmod +x`) |

### E. Environmental (not repo defects)

- **Host routing drift:** after many `docker compose down` / `up` cycles, the host
  lost its routes to both bridge subnets, so host→container traffic was reset
  while container→container worked. `sudo systemctl restart docker` rebuilt
  them.
- **`socket-init` orphan:** an init container added during debugging mounted
  `appsock` first, seeding it as `root:root` and defeating the Dockerfile's
  ownership fix; it also blocked `docker volume rm`. Removed with
  `docker compose down --remove-orphans`.
- **Docker group membership** requires a full logout/login, not a new terminal.

---

## 2. Files changed

| File | Change |
|---|---|
| `Dockerfile` | Runtime stage: `mkdir/chown/chmod /run/onionshop`; `WORKDIR /app`; copy `web/templates` |
| `cmd/onionshop/healthcheck.go` | **New** — probes `/healthz` over the UNIX socket or TCP, matching whichever `Serve` bound |
| `cmd/onionshop/main.go` | Early `os.Args[1] == "healthcheck"` branch before flag parsing |
| `docker-compose.yml` | Removed obsolete `version:`; postgres `user: "70:70"` + `PGDATA`; nginx `group_add`, `edge_net`, published 8080+8443, tmpfs modes, socket-aware healthcheck; app healthcheck via binary probe; `btcpay`/`btcpaydb` profiled off |
| `nginx/nginx.conf` | `ssl_conf_command Ciphersuites`; `pid /tmp/nginx.pid`; removed inert `ssl_ciphers` / `ssl_prefer_server_ciphers`; removed `user nginx;` |
| `internal/server/auth_handlers_test.go` | `postForm` reads prompt from `CaptchaService.Challenge()`; `extractText` deleted |
| `internal/server/friction_contract_test.go` | **New** — asserts friction field names on `/login`, `/register`, `/account` |
| `scripts/backup.sh` | exec bit |

---

## 3. Dev stack runbook

### One-time per host

```bash
export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin"
export GOTOOLCHAIN=local
sudo usermod -aG docker "$USER"        # then log out and back in
```

### TLS material (nginx will not start without it)

```bash
ONION_HOST="$(head -c 35 /dev/urandom | base32 | tr -d '=' | tr 'A-Z' 'a-z').onion"
sudo ONIONSHOP_NGINX_GID=101 ./scripts/gen-certs.sh "$ONION_HOST"
# gen-certs.sh only chgrps when GID 101 resolves in /etc/group; force numerically
sudo chgrp 101 nginx/ssl nginx/ssl/onion.crt nginx/ssl/onion.key
sudo chmod 0750 nginx/ssl && sudo chmod 0640 nginx/ssl/onion.key
```

The cert is a placeholder and authenticates nothing. Replace with the real Tor
hostname before onion testing.

### Startup

```bash
cd ~/OnionShop
set -a; . ./.env; set +a
make sqlc                                  # required before the image build
docker compose config --quiet
docker compose build app
docker compose up -d --wait postgres redis app nginx
```

### Migrations

postgres has no published port, so target its container IP (re-derive after any
recreate):

```bash
PG_IP=$(docker inspect onionshop-postgres-1 \
  --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
export DATABASE_URL="postgres://${DB_USER}:${DB_PASSWORD}@${PG_IP}:5432/${DB_NAME}?sslmode=disable"
make migrate
```

### After a daemon restart or host reboot

`depends_on` ordering is a Compose concept; the Docker daemon does not honour it.
The app can therefore come up before postgres and degrade permanently (defect 12).

```bash
docker compose restart app
docker compose logs --tail=15 app    # must NOT say "auth routes not registered"
```

### Full reset

```bash
docker compose down --remove-orphans
docker volume rm onionshop_pgdata onionshop_appsock   # never `down -v` casually
docker compose up -d --wait postgres redis app nginx
# then re-derive PG_IP and make migrate
```

---

## 4. Verification ladder

```bash
curl -sk https://127.0.0.1:8443/health                       # ok
curl -sS -o /dev/null -D - http://127.0.0.1:8080/            # 301 -> https://$host/
curl -sk -o /dev/null -w '%{http_code} %{content_type}\n' \
  https://127.0.0.1:8443/static/main.css                     # 200 text/css
curl -sk https://127.0.0.1:8443/login \
  | grep -o 'name="[a-z_]*"' | sort -u                       # friction contract
openssl s_client -connect 127.0.0.1:8443 -tls1_3 </dev/null 2>/dev/null \
  | grep -E 'Protocol|Cipher'                                # TLSv1.3 / AES_256_GCM
curl -sk --tlsv1.2 --tls-max 1.2 https://127.0.0.1:8443/health  # must FAIL (35)
```

Confirmed present on `/login`: `csrf_token`, `captcha_id`, `captcha_token`,
`captcha_answer`, `sta`, `username`, `password`.

The 301's `Location` drops the port (`https://127.0.0.1/`). Correct behind Tor,
where onion 443 maps to container 8443 — not a bug.

---

## 5. CI smoke gate (not yet added)

Append a `smoke` job to `.github/workflows/ci.yml`. Fourteen defects reached
`main` because nothing ever started a container.

Core assertions:

```bash
make sqlc
docker compose up -d --wait --wait-timeout 180 postgres redis app nginx
curl -sfk https://127.0.0.1:8443/health | grep -qx 'ok'
curl -sfk https://127.0.0.1:8443/login | grep -q 'name="captcha_token"'
test "$(curl -sk -D - -o /dev/null https://127.0.0.1:8443/login \
  | grep -ci '^x-frame-options:')" -eq 1
```

Requirements: generate an ephemeral `.env` (use `{ echo …; } > .env`, **not** an
indented heredoc, which embeds leading whitespace in secrets); generate
placeholder TLS material and `chgrp 101` numerically; name services explicitly so
`backup` and `btcpay` never start.

The header-count assertion encodes the *corrected* behaviour — apply defect 16
first or it will fail.

Add a regression test for defect 12: stop postgres, restart the app, assert the
container becomes **unhealthy** rather than healthy-but-crippled.

---

## 6. Open items, prioritised

1. **Defect 12** — fail fast or retry on missing infrastructure. A reboot can
   currently leave the market silently unable to authenticate anyone.
2. **Defect 13** — make `/healthz` a genuine readiness probe.
3. **CI smoke gate** — blocks recurrence of all of the above.
4. **Defects 16/17** — header duplication and the `/static/` inheritance gap.
5. **Defect 15** — stale socket unlink; recurs on every unclean shutdown.
6. **Defects 20–23** — toolchain and Makefile drift.
7. Pre-existing §5.6 gaps noted earlier and unchanged: compose services have no
   `mem_limit`/`pids_limit` (unlike the systemd units), and base images use
   mutable minor tags rather than pinned digests.

---

## 7. Documentation claims requiring correction

- **`SECURITY_AND_OPSEC.md` §2.3** describes the Docker Compose path as the
  "documented deployment path" with nginx terminating TLS. Before this session it
  had never started: defects 2–11 each blocked it independently, and defect 5
  meant host-side Tor (`deploy/torrc` → `127.0.0.1:8443`) could never have reached
  nginx in production either.
- **§5.1** claims `Server` is stripped by `server_tokens off` +
  `proxy_hide_header Server`. Neither removes nginx's own header (defect 18).
- **`ARCHITECTURE.md` §8.4** claims "all 92 tests pass". Five full-chain tests
  failed on the CAPTCHA-marker coupling (defect 24). Worth confirming whether
  `main` is actually green, since those tests would fail in CI too.
- **`INSTALL.md`** — the Development and Full Stack sections describe a
  `docker-compose.override.yml` with published ports on internal-only networks,
  which cannot work (defect 5), and a `make migrate` default credential that
  cannot authenticate (defect 22). Replace with §3 above.

---

## 8. Takeaway

Eleven of the fourteen defects were the same two patterns: **Debian conventions
applied to alpine images** (UIDs, symlinks, tmpfs ownership) and **assumptions
about Docker semantics that were never tested** (published ports on internal
networks, single-listener `Serve`, volume seeding order). None required deep
knowledge to fix; all required the stack to be started once.

The cheapest structural change available is the CI smoke gate. Everything else
in this document is downstream of the fact that no automated process ever asked
whether the containers actually ran.

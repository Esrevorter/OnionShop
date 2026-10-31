# Installation & Operations Guide

Audience: operators deploying the full OnionShop stack. The guided installer
(`install/install.sh`) automates everything that is **safe to automate**; destructive or
irreversible host-level hardening stays a **manual gate** by design — an installer that silently
encrypts disks or loads firewall rules is a footgun, not a feature (SECURITY_AND_OPSEC.md §5).

If you are only *developing*, you don't need this file: `make up && make run` is enough.

---

## 1. Prerequisites (manual — verify BEFORE installing)

| # | Requirement | Why / how to check |
|---|---|---|
| 1 | **LUKS2 full-disk encryption** (argon2id), separate keyslots for root vs data; swap off or encrypted | Postgres data dir, WAL, and Tor `HiddenServiceDir` must live on the encrypted volume only. `lsblk -f` should show `crypto_LUKS` under every data mount. |
| 2 | **nftables default-deny** loaded from `deploy/nftables.conf` (adapt NIC name & tor uid first) | INPUT/FORWARD/OUTPUT all default-drop; egress allowed **only** for the `tor` user. Verify: `nft list ruleset \| head -30`. |
| 3 | **Tor daemon** running with the hidden service from `deploy/torrc` (HSv3, port 80→`127.0.0.1:8443`, no exit relay) | `tor --verify-config`; `HiddenServiceDir` on the LUKS volume. |
| 4 | **Dropbear over a private admin onion** installed & tested; OpenSSH server uninstalled | Remote admin path per SECURITY_AND_OPSEC.md §5.4. Do NOT rely on this installer to open your admin channel — test it first. |
| 5 | Kernel sysctl hardening applied (`SECURITY_AND_OPSEC.md` §sysctl block) | `sysctl kernel.kptr_restrict=2` etc. |
| 6 | Docker Engine + **compose v2 plugin**, `git`, `openssl` | `docker compose version` must succeed. (`age` is only needed once you enable the backup sidecar; busybox `nc` ships inside the images.) |
| 7 | tmpfs mounts: `/tmp` (`noexec,nosuid,nodev`); compose adds RAM-only volumes itself | `mount \| grep tmpfs`. |

The installer enforces these gates mechanically — it **aborts** (exit 1) unless:
- you run under `sudo` (root) with `docker`, `openssl`, and the compose v2 plugin present (§6 deps);
- the host-hardening marker `/.onionshop-host-hardened` exists, which you may only `touch` after
  items **1–4** are verifiably done (LUKS2, nftables, Tor hidden service, Dropbear admin onion);
- `web/static/` is present (sanity check before images are built).

`--check-only` runs the phase-0 preflight audit only (deps + git provenance) and exits without any
side effects; everything past that point is gated. Note `.env` needs no pre-existing setup — the
installer creates it at mode 600 and never overwrites values already in it.

## 2. Quick start (compose path — recommended)

```bash
git clone <your-verified-source> onionshop && cd onionshop
git log --oneline -1                 # verify provenance against signed announcements
cp .env.example .env
chmod 600 .env
vi .env                              # set DB_NAME/DB_USER placeholders at minimum;
                                     # secrets can be left as <> — installer generates them
make install                         # == sudo ./install/install.sh
```

What `install.sh` does, phase by phase (all idempotent — existing values are never overwritten):

1. **Preflight** — dependency checks (`docker`, `openssl`, compose v2), git-provenance warning;
   with `--check-only` it stops here after the audit.
2. **Host-hardening gate** — aborts unless `/.onionshop-host-hardened` exists (you create it only
   after §1 items 1–4; the file's absence is deliberate friction, its presence your attestation).
3. **Secrets** — generates `DB_PASSWORD`, `REDIS_PASSWORD`, `SESSION_HASH_KEY` (64 hex from
   `openssl rand`) and defaults `DB_USER`/`DB_NAME`, appending them to `.env` (mode 600) while
   keeping every pre-existing value; warns loudly if a `GITHUB_PAT` line exists (remove it).
4. **Certificates** — runs `scripts/gen-certs.sh` *only if* `nginx/ssl/onion.crt` is absent:
   self-signed **Ed25519** cert for the onion, key in `nginx/ssl/` (mode 600, gitignored), prints
   the **SPKI fingerprint** and saves it to `/tmp/onionshop-spki.txt` (RAM only) — record it now.
   An existing cert is left untouched so published pins stay valid.
5. **Static assets sanity** — refuses to proceed if `web/static/` is missing.
6. **Build & start** — `docker compose build && docker compose up -d`, then polls until the app
   answers `/healthz` over the UNIX socket inside the container (up to ~60 s).
7. **Post-install checklist** — prints the remaining manual steps (§4 below).

Useful variants:
```bash
sudo ./install/install.sh --check-only   # dry-run: preflight + what WOULD change, no side effects
make compose-config                      # render merged compose file for review
make certs                               # regenerate cert alone (breaks SPKI pins! see script warning)
```

## 3. Bare-metal path (no Docker)

For operators who want systemd instead of containers (same threat model, more work):

1. Build: `make build` → copy `./onionshop` binary to `/usr/local/bin/`.
2. Install units: `systemd/onionshop-app.service` and `systemd/onionshop-nginx.service` →
   `/etc/systemd/system/`. They already carry the sandboxing baseline (`ProtectSystem=strict`,
   `PrivateTmp`, `NoNewPrivileges`, `SystemCallFilter`, `MemoryMax`, `RuntimeDirectory=` providing
   the tmpfs socket dir) — review `User=`/paths for your layout.
3. Run migrations against your local Postgres (`make migrate`), point `DATABASE_URL`/`REDIS_URL`
   env at localhost, and pass `--unix-socket /run/onionshop/app.sock` (units do this).
4. nginx: install `nginx/nginx.conf` as a site config; ensure `ssl/` paths and the static mount
   match, and that nginx's user is in the group owning the socket (gid 101 convention).
5. Tor: `deploy/torrc` HiddenServicePort already targets `127.0.0.1:8443`.

## 4. After installation (mandatory)

- [ ] Point Tor at nginx and reload: `HiddenServicePort 80 127.0.0.1:8443` → `systemctl reload tor`.
- [ ] Publish the **onion hostname + SPKI fingerprint** through your authenticated channel and add
      the fingerprint to `pgp.txt`/PGP-signed announcements (SECURITY_AND_OPSEC.md §1). Clients pin
      the fingerprint; regenerating the cert breaks every existing pin.
- [ ] Audit listeners: `ss -tulpn | grep -v 127.0.0.1` must return **nothing**.
- [ ] DNS-leak canary: from the app container, any attempt to resolve externally must fail
      (egress is tor-uid only): `docker compose exec app nslookup example.com || true` → expect failure.
- [ ] **Restore drill**: run `scripts/backup.sh`, copy the `.age` file off-host, restore into a
      throwaway compose project, and verify ledger invariants before holding real value.
- [ ] Test `scripts/panic-wipe.sh` in dry mode (`make panic-wipe-dry`); understand that the real
      trigger is restricted to the admin channel/console — it is never web-reachable.
- [ ] Schedule backups + warrant-canary refresh (cron/timer; see TASKS.md §Infrastructure Hardening).
- [ ] Remove any leftover credentials from `.env` that the deployment does not need (the installer
      warns about `GITHUB_PAT`; same rule as AGENTS.md — secrets are generated, never pasted in).

## 5. Operations runbook

| Task | Command |
|---|---|
| Status / logs | `docker compose ps`, `docker compose logs -f app nginx` |
| Backup now | `./scripts/backup.sh` (age-encrypted dump staged in tmpfs, plaintext shredded) |
| Restore | decrypt with the **offline** age private key, load into a throwaway DB, verify ledger invariants, only then consider a production restore (see `scripts/backup.sh` header for the exact dump format; drill first!) |
| Rotate session keys | bump `SESSION_HASH_KEY`, `docker compose up -d app` (sessions invalidate — acceptable) |
| Update images | edit tags deliberately, `docker compose pull`, `up -d`, re-audit §4 checklist |
| Emergency | `./scripts/panic-wipe.sh --i-understand-this-is-irreversible` (console/admin-onion only) |
| Uninstall (soft) | `docker compose down -v` — removes RAM-only state; disk artifacts remain |

## 6. Troubleshooting

- **"app never became healthy"** — `docker compose logs app`. Most common cause: the shared
  `appsock` volume isn't mounted at `/run/onionshop` in both containers, or the app uid/gid
  (1000:101) can't write the socket dir.
- **nginx 502** — socket missing or wrong gid; check `ls -l` inside the nginx container:
  `docker compose exec nginx ls -l /run/onionshop`.
- **Cookie dropped / login loop** — you're browsing plain HTTP. The onion serves HTTPS only;
  cookies are `Secure` by policy (AGENTS.md §4). For local dev without TLS set
  `ONIONSHOP_SECURE_COOKIES=false` — **never** in production.
- **Healthcheck flapping** — busybox `nc -U` needed inside the image; rebuild after Dockerfile
  changes (`docker compose build --no-cache app`).

## 7. Supply chain

- Build images from this repo yourself (`make install` does); do not pull third-party app images.
- Base images are tag-pinned by minor version; moving to digest pinning is tracked in TASKS.md.
- Verify the checkout against signed release announcements before installing (installer warns when
  provenance can't be checked).

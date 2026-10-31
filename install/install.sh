#!/usr/bin/env bash
# OnionShop one-shot installer — Docker path for less-experienced operators.
# Implements the phased approach in INSTALL.md: automates everything that is
# idempotent and non-destructive; STOPS with clear instructions at every step
# that is destructive or host-specific (LUKS, firewall, Tor package install).
#
# Usage:  sudo ./install/install.sh            # full guided install
#         ./install/install.sh --check-only    # dry-run dependency audit
set -euo pipefail
cd "$(dirname "$0")/.."

log()  { printf '\033[1;32m[install]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[install][WARN]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[install][FATAL]\033[0m %s\n' "$*" >&2; exit 1; }

CHECK_ONLY=0
[[ "${1:-}" == "--check-only" ]] && CHECK_ONLY=1

# ---------- 0. preflight ----------
log "phase 0: preflight checks"
[[ $EUID -eq 0 ]] || die "run with sudo (docker group membership alone is not enough for bind-mount perms)"
for c in docker openssl; do
  command -v "$c" >/dev/null || die "missing dependency: $c — see INSTALL.md §Prerequisites"
done
docker compose version >/dev/null 2>&1 || die "docker compose v2 plugin missing — see INSTALL.md §Prerequisites"
git rev-parse HEAD >/dev/null 2>&1 || warn "not a git checkout; you cannot verify build provenance (INSTALL.md §Supply chain)"

if [[ $CHECK_ONLY -eq 1 ]]; then log "all required tools present (--check-only: stopping here)"; exit 0; fi

# ---------- 1. host hardening (manual gates) ----------
log "phase 1: host-level OpSec gates (cannot be automated safely)"
if [[ ! -e /.onionshop-host-hardened ]]; then
  cat >&2 <<'GATE'
Before continuing, complete these HOST steps (details: SECURITY_AND_OPSEC.md):
  1. Full-disk LUKS2 encryption (argon2id) must be enabled on this machine.
  2. nftables default-deny policy installed (deploy/nftables.conf reference).
  3. Tor daemon installed & running with deploy/torrc's hidden-service block.
  4. OpenSSH removed; Dropbear-over-private-onion configured for admin access.
Create /.onionshop-host-hardened (touch it) ONLY when all four are done.
GATE
  die "host hardening gate not satisfied"
fi
log "host hardening marker found — proceeding"

# ---------- 2. secrets ----------
log "phase 2: generating .env secrets (existing values are never overwritten)"
umask 077
gen64() { openssl rand -hex 32; }
touch .env; chmod 600 .env
ensure_var() { # name generator
  if ! grep -q "^$1=" .env 2>/dev/null; then
    local val; val="$("$2")"
    printf '%s=%s\n' "$1" "$val" >> .env
    log "  set $1"
  else
    log "  kept existing $1"
  fi
}
ensure_var DB_USER        sh -c 'echo onionshop'
ensure_var DB_PASSWORD    gen64
ensure_var DB_NAME        sh -c 'echo onionshop'
ensure_var REDIS_PASSWORD gen64
ensure_var SESSION_HASH_KEY gen64
if ! grep -q '^AGE_PUBLIC_KEY=' .env; then
  warn "AGE_PUBLIC_KEY missing. Generate an age keypair OFFLINE and add the PUBLIC half:"
  warn "  age-keygen -o age.key   (keep age.key on offline media only)"
  warn "Then append:  AGE_PUBLIC_KEY=age1... to .env  before starting the backup sidecar."
fi
grep -q '^GITHUB_PAT=' .env && warn ".env contains GITHUB_PAT — deployment does not need it; remove it"

# ---------- 3. TLS certificate + SPKI pin ----------
log "phase 3: nginx TLS material"
if [[ ! -e nginx/ssl/onion.crt ]]; then
  ./scripts/gen-certs.sh | tee /tmp/onionshop-spki.txt
  log "SPKI fingerprint saved to /tmp/onionshop-spki.txt (RAM only) — record it now"
else
  log "certificate already present; leaving SPKI pin stable (use scripts/gen-certs.sh --force only deliberately)"
fi

# ---------- 4. static assets ----------
log "phase 4: web/static sanity"
[[ -f web/static/.gitkeep ]] || die "web/static missing — run from the repo root"
if compgen -G 'web/static/*' >/dev/null 2>&1 || ls web/static >/dev/null 2>&1; then :; fi

# ---------- 5. build & start ----------
log "phase 5: building images and starting the stack"
docker compose build
docker compose up -d
log "waiting for app health..."
for i in $(seq 1 30); do
  if docker compose exec -T app wget -q --spider http://127.0.0.1:8080/healthz 2>/dev/null; then break; fi
  sleep 2
  [[ $i -eq 30 ]] && die "app never became healthy — check: docker compose logs app"
done
log "stack is healthy"

# ---------- 6. post-install guidance ----------
cat <<FINAL

Install complete. Remaining MANUAL steps (see INSTALL.md §After installation):
  * Point Tor's HiddenServicePort at 127.0.0.1:8443 (deploy/torrc) and reload tor.
  * Record the onion hostname + SPKI fingerprint; publish over your authenticated channel.
  * Run a restore drill with scripts/backup.sh output before trusting backups.
  * Verify nothing listens publicly:  ss -tulpn | grep -v 127.0.0.1
FINAL
log "DONE"

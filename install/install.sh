#!/usr/bin/env bash
# Guided development/preview installer. This script does NOT establish that a
# host, TLS certificate, firewall, or payment configuration is production safe.
set -euo pipefail
cd "$(dirname "$0")/.."

log()  { printf '\033[1;32m[install]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[install][WARN]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[install][FATAL]\033[0m %s\n' "$*" >&2; exit 1; }

CHECK_ONLY=0
[[ "${1:-}" == "--check-only" ]] && CHECK_ONLY=1

log "phase 0: preflight checks"
[[ $EUID -eq 0 ]] || die "run with sudo (Docker bind-mount permissions require root)"
for c in docker openssl; do
  command -v "$c" >/dev/null || die "missing dependency: $c — see INSTALL.md"
done
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin missing"
git rev-parse HEAD >/dev/null 2>&1 || warn "not a Git checkout; provenance cannot be checked"
if [[ $CHECK_ONLY -eq 1 ]]; then
  log "required tools present (--check-only: no side effects)"
  exit 0
fi

log "phase 1: host safety gates"
if [[ ! -e /.onionshop-host-hardened ]]; then
  cat >&2 <<'GATE'
Complete and verify host-specific controls before proceeding: encrypted storage,
firewall/egress policy, Tor configuration, private admin access, and recovery.
This marker is an operator attestation, not an automated security test.
GATE
  die "host-hardening marker not present"
fi
log "host-hardening marker found (attestation only)"

log "phase 2: private environment setup"
umask 077
touch .env
chmod 600 .env
ensure_var() {
  local name="$1" generator="$2"
  if ! grep -q "^${name}=" .env 2>/dev/null; then
    local value
    value="$(eval "$generator")"
    printf '%s=%s\n' "$name" "$value" >> .env
    log "set $name"
  else
    log "kept existing $name"
  fi
}
gen64() { openssl rand -hex 32; }
ensure_var DB_USER "printf onionshop"
ensure_var DB_PASSWORD gen64
ensure_var DB_NAME "printf onionshop"
ensure_var REDIS_PASSWORD gen64
ensure_var SESSION_HASH_KEY gen64
if ! grep -q '^AGE_PUBLIC_KEY=' .env 2>/dev/null; then
  die "AGE_PUBLIC_KEY is required by the configured backup sidecar; generate its private identity offline and set the public recipient in .env"
fi
if ! grep -q '^BTCPAY_API_KEY=' .env 2>/dev/null; then
  warn "BTCPAY_API_KEY is unset; configure a test/production BTCPay credential as appropriate before using checkout"
fi
grep -q '^GITHUB_PAT=' .env && warn ".env contains GITHUB_PAT; remove this unrelated credential"

log "phase 3: TLS material"
if [[ ! -s nginx/ssl/onion.crt || ! -s nginx/ssl/onion.key ]]; then
  die "TLS certificate/key pair required in nginx/ssl. scripts/gen-certs.sh creates a generic self-signed local-test certificate only; it is not onion-authenticated. Production deployment is blocked until a supported TLS identity is provisioned and independently verified."
fi
warn "TLS certificate files exist, but this installer cannot verify onion-key authorization or browser trust. Do not treat file presence as production TLS validation."

log "phase 4: static assets"
[[ -f web/static/.gitkeep ]] || die "web/static missing — run from repository root"

log "phase 5: build and start preview stack"
docker compose build
docker compose up -d
log "waiting for nginx health endpoint through the app UNIX socket..."
for i in $(seq 1 30); do
  if docker compose exec -T nginx wget --no-check-certificate --quiet --tries=1 -O /dev/null https://127.0.0.1:8443/health 2>/dev/null; then
    break
  fi
  sleep 2
  [[ $i -eq 30 ]] && die "nginx/app health check failed — inspect docker compose logs"
done
log "preview stack health check passed; this is not production approval"

cat <<'FINAL'

Preview stack started. Before any production use, complete and record every gate
in PRODUCTION_READINESS.md. In particular, do not publish or pin the generated
SPKI hash, do not bypass TLS warnings, and verify Tor mappings, egress, image
provenance, and an offline-key restore drill.
FINAL
log "DONE"

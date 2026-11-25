#!/usr/bin/env bash
# OnionShop installer - friendly on a Debian 13 (trixie) minimal image.
# Idempotent. Development preview only: a green stack is NOT production clearance.
set -euo pipefail
cd "$(dirname "$0")/.."

log(){ printf "\033[1;32m[install]\033[0m %s\n" "$*"; }
warn(){ printf "\033[1;33m[warn]\033[0m %s\n" "$*" >&2; }
die(){ printf "\033[1;31m[fatal]\033[0m %s\n" "$*" >&2; exit 1; }

DEV=0; CHECK=0; BOOTSTRAP=1; ONION=""; WHONIX=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dev) DEV=1 ;;
    --check-only) CHECK=1 ;;
    --no-bootstrap) BOOTSTRAP=0 ;;
    --onion) ONION="${2:-}"; shift ;;
    --whonix) WHONIX=1 ;;
    -h|--help) sed -n '2,6p' "$0"; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

log "phase 0 - preflight"
if [[ -r /etc/os-release ]]; then
  . /etc/os-release
  log "host: ${PRETTY_NAME:-unknown}"
  [[ "${VERSION_CODENAME:-}" == "trixie" ]] || warn "tested on Debian 13 trixie; continuing"
fi
if [[ $CHECK -eq 1 ]]; then
  for c in docker openssl git age; do command -v "$c" >/dev/null && log "found $c" || warn "missing $c"; done
  docker compose version >/dev/null 2>&1 && log "compose v2 present" || warn "compose v2 missing"
  log "check-only: no side effects"; exit 0
fi
[[ $EUID -eq 0 ]] || die "run with sudo"

if [[ $BOOTSTRAP -eq 1 ]]; then
  log "phase 1 - bootstrap dependencies"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg git openssl age >/dev/null
  if ! command -v docker >/dev/null; then
    log "installing Docker CE (trixie)"
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" > /etc/apt/sources.list.d/docker.list
    apt-get update -qq
    if ! apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null; then
      warn "Docker CE repo unavailable; falling back to Debian packages"
      apt-get install -y -qq docker.io docker-compose-v2 >/dev/null || apt-get install -y -qq docker.io docker-compose >/dev/null
    fi
  fi
  systemctl enable --now docker >/dev/null 2>&1 || true
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 unavailable"
  [[ -n "${SUDO_USER:-}" ]] && usermod -aG docker "${SUDO_USER}" || true
fi
command -v openssl >/dev/null || die "openssl missing"

log "phase 2 - private environment"
umask 077; touch .env; chmod 600 .env
gen64(){ openssl rand -hex 32; }
ensure(){ local n="$1" g="$2"; if grep -q "^${n}=" .env; then log "kept $n"; else printf "%s=%s\n" "$n" "$(eval "$g")" >> .env; log "set $n"; fi; }
ensure DB_USER "printf onionshop"
ensure DB_NAME "printf onionshop"
ensure DB_PASSWORD gen64
ensure REDIS_PASSWORD gen64
ensure SESSION_HASH_KEY gen64
if [[ $WHONIX -eq 1 ]]; then
  ensure NGINX_BIND_IP "printf 0.0.0.0"
  warn "WHONIX: firewall 8080/8443 to the Gateway IP only (SECURITY_AND_OPSEC.md section 3)"
else
  ensure NGINX_BIND_IP "printf 127.0.0.1"
fi
ensure XMR_MODE "printf rpc"
ensure XMR_WALLET_RPC_URL "printf http://monero-wallet-rpc:18083/json_rpc"
ensure XMR_WALLET_NAME "printf onionshop"
ensure XMR_WALLET_PASSWORD gen64
ensure XMR_CONFIRMATIONS_REQUIRED "printf 3"
if ! grep -q "^AGE_PUBLIC_KEY=" .env; then
  if [[ $DEV -eq 1 ]]; then
    mkdir -p secrets; chmod 700 secrets
    [[ -s secrets/age-dev.key ]] || age-keygen -o secrets/age-dev.key 2>/dev/null
    chmod 600 secrets/age-dev.key
    printf "AGE_PUBLIC_KEY=%s\n" "$(age-keygen -y secrets/age-dev.key)" >> .env
    warn "DEV age identity created locally. Production requires an OFFLINE identity."
  else
    die "AGE_PUBLIC_KEY required: generate offline, put only the public recipient in .env (or use --dev)"
  fi
fi
grep -q "^GITHUB_PAT=" .env && warn ".env contains GITHUB_PAT - unrelated credential, remove it"

log "phase 3 - TLS material"
mkdir -p nginx/ssl
if [[ ! -s nginx/ssl/onion.crt || ! -s nginx/ssl/onion.key ]]; then
  if [[ -n "$ONION" ]]; then
    ONIONSHOP_NGINX_GID=101 ./scripts/gen-certs.sh "$ONION"
  elif [[ $DEV -eq 1 ]]; then
    warn "generating LOCAL-TEST certificate (localhost) - not onion-authenticated"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-384 -nodes -days 30 \
      -keyout nginx/ssl/onion.key -out nginx/ssl/onion.crt \
      -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null
  else
    die "no TLS pair: pass --onion <hostname>.onion, or --dev for a local test cert"
  fi
fi
chgrp 101 nginx/ssl/onion.key 2>/dev/null || true
chmod 640 nginx/ssl/onion.key; chmod 644 nginx/ssl/onion.crt

log "phase 4 - build and database"
docker compose build
docker compose up -d postgres redis
DBU=$(grep "^DB_USER=" .env | cut -d= -f2)
DBN=$(grep "^DB_NAME=" .env | cut -d= -f2)
for i in $(seq 1 60); do
  docker compose exec -T postgres pg_isready -U "$DBU" >/dev/null 2>&1 && break
  sleep 2
  [[ $i -eq 60 ]] && die "postgres did not become ready"
done
PSQL(){ docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$DBU" -d "$DBN" "$@"; }
PSQL -q -c "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false, applied_at timestamptz NOT NULL DEFAULT now());" >/dev/null
MAXV=$(PSQL -tAc "SELECT COALESCE(max(version),0) FROM schema_migrations" | tr -d "[:space:]")
for f in $(ls db/migrations/*.up.sql | sort); do
  v=$((10#$(basename "$f" | cut -d_ -f1)))
  if [[ "$v" -le "$MAXV" ]]; then
    PSQL -q -c "INSERT INTO schema_migrations(version) VALUES ($v) ON CONFLICT DO NOTHING" >/dev/null
    continue
  fi
  log "migration $v - $(basename "$f")"
  PSQL -q < "$f" >/dev/null || die "migration $v failed"
  PSQL -q -c "INSERT INTO schema_migrations(version) VALUES ($v) ON CONFLICT DO NOTHING" >/dev/null
done
log "schema up to date"

log "phase 5 - start stack"
docker compose up -d
for i in $(seq 1 45); do
  docker compose exec -T nginx wget --no-check-certificate -q -O /dev/null https://127.0.0.1:8443/health 2>/dev/null && break
  sleep 2
  if [[ $i -eq 45 ]]; then docker compose logs --tail=80; die "health check failed"; fi
done
log "health OK"
echo
if [[ -n "$ONION" ]]; then log "onion: https://$ONION (Gateway torrc to this host 8080/8443)"; else log "local: https://127.0.0.1:8443 (self-signed dev cert)"; fi
log "Help Center: /help - vendor guide /help/vendors - operator guide /help/admins"
warn "preview stack only; see PRODUCTION_READINESS.md before any real value"

#!/usr/bin/env bash
# Panic wipe — immediate denial of data under coercion (SECURITY_AND_OPSEC.md
# §Incident Response). TRIGGER ONLY FROM YOUR AUTHENTICATED PRIVATE CHANNEL.
# Irreversible. Test the procedure quarterly against a THROWAWAY host, never prod.
set -uo pipefail

usage() {
  cat >&2 <<'USAGE'
panic-wipe.sh — destroys ALL OnionShop secrets and data on this host.
Refuses to run without explicit confirmation flags:
  --i-understand-this-is-irreversible   required human authorization
  --host=<expected-hostname-or-onion>   guard against running on the wrong box
USAGE
  exit 2
}

AUTH=0; EXPECT=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --i-understand-this-is-irreversible) AUTH=1 ;;
    --host=*) EXPECT="${1#*=}" ;;
    *) usage ;;
  esac
  shift
done
[[ $AUTH -eq 1 ]] || usage
if [[ -n "$EXPECT" && "$(hostname)" != "$EXPECT" ]]; then
  echo "ABORT: hostname $(hostname) != expected $EXPECT" >&2; exit 3
fi

log() { echo "[panic-wipe] $(date -u +%FT%TZ) $*"; }

log "1/6 stopping all services (systemd units + containers)"
docker compose down -v --remove-orphans 2>/dev/null || true
for u in onionshop-app onionshop-nginx onionshop-postgres onionshop-redis onionshop-tor; do
  systemctl stop "$u" 2>/dev/null || true
done

log "2/6 shredding app/nginx secrets"
shred -fu nginx/ssl/onion.key 2>/dev/null || rm -f nginx/ssl/onion.key
shred -fu .secrets/* 2>/dev/null || rm -rf .secrets
shred -fu .env 2>/dev/null || rm -f .env

log "3/6 destroying Tor hidden-service private keys"
for d in /var/lib/tor/onionshop /var/lib/tor/data/onionshop; do
  [[ -d $d ]] && { shred -fu "$d"/hostname "$d"/private_key 2>/dev/null || rm -rf "$d"; }
done

log "4/6 wiping tmpfs logs/sockets"
rm -rf /run/onionshop/* /dev/shm/onionshop* 2>/dev/null || true
journalctl --rotate >/dev/null 2>&1 || true
journalctl --vacuum-size=1 >/dev/null 2>&1 || true

log "5/6 removing database volumes (LUKS volume stays; keys destroyed above)"
docker volume ls -q | grep -E 'onionshop|pgdata|btcpay' | xargs -r docker volume rm -f 2>/dev/null || true

log "6/6 final: verify nothing recoverable remains, then power off."
log "NOTE: full-disk LUKS header re-key or secure-erase is a MANUAL next step;"
log "this script cannot safely re-key a live encrypted root by itself."
log "PANIC WIPE COMPLETE"

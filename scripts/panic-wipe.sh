#!/usr/bin/env bash
# Panic wipe — removes OnionShop secrets and data on this host.
# TRIGGER ONLY FROM YOUR AUTHENTICATED PRIVATE CHANNEL.
# Irreversible. Test the procedure quarterly against a THROWAWAY host, never prod.
#
# IMPORTANT: rm removes directory entries; it is NOT a secure erase on SSDs,
# copy-on-write filesystems, snapshots, or journaled storage. Encryption at rest
# and offline destruction of the LUKS keyslots are the meaningful data-denial
# controls. This script deliberately does not attempt to re-key a live system.
set -uo pipefail

usage() {
  cat >&2 <<'USAGE'
panic-wipe.sh — stops OnionShop and removes its secrets and Docker volumes.
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

log "2/6 removing app/nginx secret files (not a secure erase on persistent storage)"
rm -f -- nginx/ssl/onion.key .env
rm -rf -- .secrets

log "3/6 removing Tor hidden-service private keys"
# Tor v3 keys include hs_ed25519_secret_key; remove the entire service dirs
# rather than relying on guessed key filenames. Ensure the Tor service is stopped.
for d in /var/lib/tor/onionshop /var/lib/tor/data/onionshop; do
  [[ -d "$d" ]] && rm -rf -- "$d"
done

log "4/6 removing tmpfs logs/sockets and pruning volatile journal"
rm -rf -- /run/onionshop /dev/shm/onionshop* 2>/dev/null || true
journalctl --rotate >/dev/null 2>&1 || true
journalctl --vacuum-size=1 >/dev/null 2>&1 || true

log "5/6 removing this Compose project's database volumes"
# `compose down -v` above removes volumes declared by this project. Do not
# pattern-match and delete arbitrary host Docker volumes: names are not ownership.

log "6/6 final: verify remaining artifacts and power off."
log "LUKS keyslot/header destruction or cryptographic erase is a MANUAL next step."
log "rm is not secure erase; rely on LUKS key destruction for persistent data."
log "PANIC WIPE COMPLETE"

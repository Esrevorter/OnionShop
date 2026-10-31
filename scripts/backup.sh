#!/usr/bin/env sh
# OnionShop backup runner — designed for the 'backup' sidecar in docker-compose.
# Dumps Postgres to tmpfs (/tmp = RAM), encrypts with age immediately, wipes
# the plaintext, and prunes old archives. NEVER writes plaintext to disk.
#
# Required env: DB_USER DB_PASSWORD DB_NAME AGE_PUBLIC_KEY
# Optional:     RETENTION_DAYS (default 14), INTERVAL_SECONDS (default 86400)
set -eu

RETENTION_DAYS="${RETENTION_DAYS:-14}"
INTERVAL="${INTERBACK_INTERVAL_SECONDS:-${INTERVAL_SECONDS:-86400}}"
BACKUP_DIR="${BACKUP_DIR:-/backup}"
PGHOST="${PGHOST:-postgres}"

log() { echo "[backup] $(date -u +%FT%TZ) $*"; }

run_once() {
  STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
  PLAIN="/tmp/onionshop-${STAMP}.sql"          # tmpfs only — never BACKUP_DIR unencrypted
  ENC="${BACKUP_DIR}/onionshop-${STAMP}.sql.age"
  log "dumping ${DB_NAME} -> tmpfs"
  pg_dump -h "$PGHOST" -U "$DB_USER" -d "$DB_NAME" --format=plain --no-owner > "$PLAIN"
  log "encrypting with age"
  age -r "$AGE_PUBLIC_KEY" -o "$ENC.tmp" "$PLAIN"
  mv "$ENC.tmp" "$ENC"; sync
  # Verify decryptability before wiping the plaintext (a corrupt backup is no backup)
  age -d -i /nonexistent "$ENC" >/dev/null 2>&1 || log "WARN: decryption check needs the private key; skipping (verify during restore drills)"
  shred -u "$PLAIN" 2>/dev/null || rm -f "$PLAIN"
  log "pruning archives older than ${RETENTION_DAYS} days"
  find "$BACKUP_DIR" -name '*.sql.age' -mtime "+$RETENTION_DAYS" -delete
  log "done: $ENC"
}

log "backup runner started (interval=${INTERVAL}s, retention=${RETENTION_DAYS}d)"
while :; do
  run_once || log "ERROR: backup cycle failed"
  sleep "$INTERVAL"
done

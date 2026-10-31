#!/usr/bin/env bash
# Generates the self-signed TLS 1.3 certificate nginx terminates with, and
# prints the SPKI fingerprint users must pin (SECURITY_AND_OPSEC.md §TLS).
# Idempotent: refuses to overwrite an existing cert unless --force (changing
# the key silently breaks every client's SPKI pin — distribute deliberately).
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."

CERT_DIR="nginx/ssl"
KEY="$CERT_DIR/onion.key"
CRT="$CERT_DIR/onion.crt"
DAYS=825          # < 2 years; rotate on a planned schedule with advance notice
FORCE=0
[[ "${1:-}" == "--force" ]] && FORCE=1

if [[ -e "$KEY" || -e "$CRT" ]]; then
  if [[ $FORCE -eq 0 ]]; then
    echo "ERROR: $KEY/$CRT already exist. Re-run with --force to replace." >&2
    echo "WARNING: replacing the key changes the SPKI fingerprint — every pinned" >&2
    echo "client must be notified BEFORE you do this." >&2
    exit 1
  fi
  shred -u "$KEY" 2>/dev/null || rm -f "$KEY"
fi

mkdir -p "$CERT_DIR"; chmod 700 "$CERT_DIR"
openssl req -x509 -newkey ed25519 -keyout "$KEY" -out "$CRT" \
  -days "$DAYS" -nodes -subj "/CN=onionshop" \
  -addext "subjectAltName=IP:127.0.0.1,IP:::1" 2>/dev/null
chmod 600 "$KEY"; chmod 644 "$CRT"

SPKI=$(openssl x509 -in "$CRT" -pubkey -noout \
  | openssl pkey -pubin -outform der \
  | openssl dgst -sha256 -binary | openssl base64)

echo "OK: certificate written to $CRT"
echo "OK: private key at $KEY (mode 600, never copy off-host, never commit)"
echo
echo "Pin this SPKI SHA-256 fingerprint in your client configuration:"
echo "  sha256/${SPKI}"
echo
echo "Record it alongside the .onion address in your vendor notes."

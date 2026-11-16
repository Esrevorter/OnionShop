#!/usr/bin/env bash
# Generate a self-signed TLS 1.3 certificate for the exact v3 .onion hostname.
# This TLS key is separate from the Tor v3 Ed25519 service identity and is not a
# browser trust anchor. The purpose is to provide a TLS layer in front of the
# onion service while preserving the product policy of never shipping a CA-issued
# cert or SPKI pin. See INSTALL.md and SECURITY_AND_OPSEC.md.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."

usage() {
  echo "Usage: $0 <v3-onion-hostname> [--force]" >&2
  echo "Example: $0 32character...onion" >&2
  exit 2
}

HOST=""
FORCE=0

case $# in
  0)
    usage
    ;;
  1)
    HOST="${1,,}"
    ;;
  2)
    if [[ "$2" != "--force" ]]; then
      usage
    fi
    HOST="${1,,}"
    FORCE=1
    ;;
  *)
    usage
    ;;
 esac

if [[ ! "$HOST" =~ ^[a-z2-7]{56}\.onion$ ]]; then
  echo "ERROR: expected a valid v3 onion hostname: 56 lowercase base32 chars + .onion" >&2
  echo "Got: $HOST" >&2
  exit 1
fi

CERT_DIR="nginx/ssl"
KEY="$CERT_DIR/onion.key"
CRT="$CERT_DIR/onion.crt"
NGINX_GID="${ONIONSHOP_NGINX_GID:-101}"

if [[ -e "$KEY" || -e "$CRT" ]]; then
  if [[ $FORCE -eq 0 ]]; then
    echo "ERROR: $KEY and/or $CRT already exist; refusing to overwrite." >&2
    echo "Use: $0 $HOST --force" >&2
    exit 1
  fi
  rm -f -- "$KEY" "$CRT"
fi

mkdir -p "$CERT_DIR"
chmod 0750 "$CERT_DIR"

TMP_KEY="$CERT_DIR/onion.key.tmp"
TMP_CRT="$CERT_DIR/onion.crt.tmp"
trap 'rm -f -- "$TMP_KEY" "$TMP_CRT"' EXIT

# Generate a separate ECDSA P-384 TLS key, not the Tor Ed25519 identity key.
openssl req -x509 -newkey ec \
  -pkeyopt ec_paramgen_curve:secp384r1 \
  -sha256 -days 825 -nodes \
  -keyout "$TMP_KEY" -out "$TMP_CRT" \
  -subj "/CN=$HOST" \
  -addext "subjectAltName=DNS:$HOST" \
  -addext "basicConstraints=critical,CA:FALSE" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth" >/dev/null 2>&1

openssl x509 -in "$TMP_CRT" -noout -checkhost "$HOST" >/dev/null

CERT_PUB=$(openssl x509 -in "$TMP_CRT" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256 | awk '{print $2}')
KEY_PUB=$(openssl pkey -in "$TMP_KEY" -pubout -outform DER | openssl dgst -sha256 | awk '{print $2}')
if [[ "$CERT_PUB" != "$KEY_PUB" ]]; then
  echo "ERROR: generated certificate/key mismatch; aborting." >&2
  exit 1
fi

mv -- "$TMP_KEY" "$KEY"
mv -- "$TMP_CRT" "$CRT"

# Keep the directory/key aligned with nginx's configured group in container or host deployments.
if getent group "$NGINX_GID" >/dev/null 2>&1; then
  chgrp "$NGINX_GID" "$CERT_DIR" "$KEY" "$CRT" || true
fi
chmod 0750 "$CERT_DIR"
chmod 0640 "$KEY"
chmod 0644 "$CRT"
trap - EXIT

echo "Generated TLS certificate for exact onion hostname: $HOST"
echo "Certificate: $CRT"
echo "Private key: $KEY"
echo "Exact DNS SAN: $HOST"
echo "Note: this is a separate ECDSA P-384 TLS key, not the Tor Ed25519 identity key."
echo "No SPKI fingerprint is printed; do not pin or publish a certificate hash for this project."

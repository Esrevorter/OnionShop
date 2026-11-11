#!/usr/bin/env bash
# Create a self-signed HTTPS leaf certificate whose DNS SAN is exactly the
# OnionShop v3 onion hostname. The private key is a separate ECDSA P-384 key;
# it is NOT the Tor onion-service Ed25519 key and does NOT prove onion identity.
# Tor authenticates the onion endpoint; this certificate adds a TLS layer.
# No SPKI hash is printed or intended to be pinned.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."

usage() {
  echo "Usage: $0 <service-v3-onion-hostname> [--force]" >&2
  echo "Set ONIONSHOP_NGINX_GID if nginx uses a host-mapped group other than 101." >&2
  exit 2
}

[[ $# -ge 1 && $# -le 2 ]] || usage
HOST=${1,,}
FORCE=0
if [[ $# -eq 2 ]]; then
  [[ "$2" == "--force" ]] || usage
  FORCE=1
fi
if [[ ! "$HOST" =~ ^[a-z2-7]{56}\.onion$ ]]; then
  echo "ERROR: expected a v3 onion hostname (56 lowercase base32 chars plus .onion)" >&2
  exit 1
fi

CERT_DIR="nginx/ssl"
KEY="$CERT_DIR/onion.key"
CRT="$CERT_DIR/onion.crt"
NGINX_GID=${ONIONSHOP_NGINX_GID:-101}

if [[ -e "$KEY" || -e "$CRT" ]]; then
  if [[ $FORCE -ne 1 ]]; then
    echo "ERROR: certificate or key already exists; refusing to overwrite." >&2
    echo "Use --force only for a planned rotation after reviewing deployment impact." >&2
    exit 1
  fi
  echo "WARNING: replacing TLS key/certificate; no SPKI pinning is used." >&2
  rm -f -- "$KEY" "$CRT"
fi

mkdir -p "$CERT_DIR"
# nginx runs as uid/gid 101 in the supplied compose configuration. The
# directory and key are group-readable only to that group. Adjust the group id
# to match a reviewed host/container mapping if yours differs.
chmod 0750 "$CERT_DIR"
TMP_KEY="$CERT_DIR/onion.key.tmp"
TMP_CRT="$CERT_DIR/onion.crt.tmp"
trap 'rm -f -- "$TMP_KEY" "$TMP_CRT"' EXIT

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 \
  -sha256 -days 825 -nodes \
  -keyout "$TMP_KEY" -out "$TMP_CRT" \
  -subj "/CN=$HOST" \
  -addext "subjectAltName=DNS:$HOST" \
  -addext "basicConstraints=critical,CA:FALSE" \
  -addext "keyUsage=critical,digitalSignature" \
  -addext "extendedKeyUsage=serverAuth"

openssl x509 -in "$TMP_CRT" -noout -checkhost "$HOST" >/dev/null
cert_pub=$(openssl x509 -in "$TMP_CRT" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)
key_pub=$(openssl pkey -in "$TMP_KEY" -pubout -outform DER | openssl dgst -sha256)
[[ "$cert_pub" == "$key_pub" ]] || { echo "ERROR: generated certificate/key mismatch" >&2; exit 1; }

mv -- "$TMP_KEY" "$KEY"
mv -- "$TMP_CRT" "$CRT"
chgrp "$NGINX_GID" "$CERT_DIR" "$KEY"
chmod 0750 "$CERT_DIR"
chmod 0640 "$KEY"
chmod 0644 "$CRT"
trap - EXIT

cat <<EOF
Created self-signed TLS certificate:
  certificate: $CRT
  private key: $KEY (group $NGINX_GID, mode 0640)
  exact DNS SAN: $HOST

This TLS key is a separate ECDSA P-384 key, not the Tor Ed25519 identity key.
The SAN matches the onion hostname, but the certificate is self-signed. Tor
Browser-only support is a project policy, not an enforceable browser check.
Test the exact supported Tor Browser release. If it displays a certificate
warning, STOP: do not click through, import an unverified root, or substitute a
CA certificate or SPKI pin. Tor's onion protocol authenticates the endpoint
independently of this TLS certificate.
EOF

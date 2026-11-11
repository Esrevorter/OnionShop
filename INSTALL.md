# Installation & Operations Guide

This repository includes a development compose stack and a guided preview installer. Neither certifies production readiness. OnionShop supports Tor Browser over a Tor v3 onion service only; mainstream browsers are unsupported by project policy (not technically enforceable).

## TLS and Tor Browser policy

Use a self-signed ECDSA P-384 certificate with the exact service v3 onion hostname in its `subjectAltName` DNS entry. Its private key is separate from the Tor Ed25519 service identity. Tor authenticates the onion endpoint at the protocol layer; the TLS certificate does not cryptographically match that onion key. The exact SAN is necessary for hostname matching but does not, by itself, establish that Tor Browser will accept a self-signed certificate without warning.

**Do not use a public CA-issued certificate for this project. Do not publish, store, or enforce SPKI/certificate hash pins. Do not click through Tor Browser certificate warnings.** Verify behavior with the exact supported Tor Browser release and stop deployment if it warns; do not instruct operators or users to bypass the warning. The generated self-signed key is a TLS key, not the onion authentication key.

Once Tor has generated the public v3 onion hostname, provision the certificate and key on the host:

```sh
sudo ONIONSHOP_NGINX_GID=101 ./scripts/gen-certs.sh '<56-character-v3-hostname>.onion'
```

The script refuses to overwrite an existing pair unless `--force` is provided; planned rotation must be reviewed. It creates `nginx/ssl/onion.crt` and `nginx/ssl/onion.key`, with the directory/key group-readable for nginx's configured group. Review host/container GID mapping and permissions before starting the proxy. The cert generator prints no SPKI fingerprint.

Inspect the certificate and exact SAN:

```sh
openssl x509 -in nginx/ssl/onion.crt -noout -subject -ext subjectAltName -dates
openssl x509 -in nginx/ssl/onion.crt -noout -checkhost '<56-character-v3-hostname>.onion'
openssl pkey -in nginx/ssl/onion.key -check -noout
```

These checks verify certificate contents/key integrity; they do **not** prove Tor Browser acceptance or bind the TLS key to the onion identity. Complete the browser test on a clean, supported Tor Browser profile without exceptions or bypasses. If a browser warning appears, stop: the deployment is not ready under this policy.

## Development prerequisites

- Go toolchain matching `go.mod` (Go 1.25.x for the current CI configuration)
- sqlc v1.27.0 (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0`)
- Docker Engine with Compose v2 for the compose stack

Run tests and build with:

```sh
make test
make build
```

`make build` and `make test` invoke `sqlc generate`; generated code is ignored in Git. Compose interpolation requires a private `.env` file. Use non-production credentials and do not expose development services to a public interface.

## Deployment review order

1. Prepare and test host encryption, swap, firewall, Tor, and admin access on disposable infrastructure. Do not blindly apply host templates remotely.
2. Prepare secrets with unique credentials and an age recipient whose private identity stays offline.
3. Set up the Tor v3 service and record its hostname locally; keep public and private admin onion identities separate.
4. Generate the self-signed certificate with the exact onion SAN as above, verify file permissions, and test the exact Tor Browser release. No warning bypasses are permitted.
5. Verify image digests/platforms and review Compose network, socket, database, Redis, and BTCPay configuration.
6. Confirm onion port 80 reaches nginx's redirect listener on host loopback 8080, and onion port 443 reaches TLS on host loopback 8443. Check both endpoints in Tor Browser.
7. Run migrations, application tests, payment/escrow workflow tests, egress checks, and a complete offline-key backup restore drill on disposable systems.
8. Record results and operator sign-off. See `PRODUCTION_READINESS.md`; the installer is not a security certification.

## Compose validation

With a complete `.env`:

```sh
docker compose config --quiet
docker compose build
docker compose ps
```

Configuration parsing is not a security review.

## Backup and restore

Backups are encrypted to the configured age recipient. Keep the corresponding private identity offline and separate from the backup host. A successful backup command alone does not prove recoverability; restore into an isolated environment and verify data, ledger invariants, and audit continuity.

## Panic procedure

`scripts/panic-wipe.sh` is irreversible. `rm`/`shred` cannot guarantee erasure from SSDs, snapshots, copy-on-write filesystems, or journals. Persistent data denial depends on separately tested cryptographic key destruction. Test only on a throwaway host.

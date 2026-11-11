# Installation & Operations Guide

This repository includes a development compose stack and a guided installer, but they are **not a certification of production readiness**. Read `PRODUCTION_READINESS.md` and resolve every production gate before handling real users, credentials, or funds. The current TLS generator makes a generic self-signed certificate; it is not onion-bound and its SPKI output is not an authentication mechanism.

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

## Deployment order (review-only until production gates are resolved)

1. **Prepare the host.** Design and verify LUKS2, swap, nftables, Tor, and admin access on a disposable system first. Do not blindly apply `deploy/nftables.conf`, `deploy/torrc`, or the systemd units to a remote host.
2. **Prepare secrets offline.** Generate an age keypair off-host; put only the recipient/public key in `AGE_PUBLIC_KEY`. Keep the identity/private key offline. Configure unique database, Redis, session, and BTCPay credentials in a mode-0600 `.env`. The compose file includes a backup sidecar, so the public age recipient is required for it to function.
3. **Review and validate configuration.** Confirm the onion hostname, host-to-container port mappings, storage mounts, network egress, users/groups, and every image digest against authoritative registry metadata. `docker compose config --quiet` validates syntax/interpolation only; it does not validate security or service compatibility.
4. **Provide TLS material.** The current `scripts/gen-certs.sh` output is self-signed and has no onion hostname SAN or onion-key authorization. It is suitable only for local testing. Production needs a supported, browser-compatible onion-authentication plan and matching certificate provisioning before nginx is exposed. Do not publish or pin the generated SPKI hash.
5. **Build and test on an isolated host.** Run migrations, verify the application socket and proxy path, exercise payment polling against a test BTCPay instance, and verify backups/restores before any real value is introduced.
6. **Bring up Tor only after checks pass.** Tor port 80 should reach nginx's HTTP redirect listener (host loopback port 8080); onion port 443 should reach nginx TLS (host loopback port 8443). Verify both paths from Tor Browser and verify certificate behavior without bypassing browser warnings.
7. **Verify and monitor.** Inspect published listeners and routes, test that application/Postgres/Redis have no unintended egress, run a complete restore drill, and keep operational/canary procedures current.

The sequence above documents required operator work; it does not claim all these controls are currently automated or fully implemented. The current installer must not be used to bypass these gates.

## Compose validation

With a complete `.env`:

```sh
docker compose config --quiet
docker compose build
docker compose ps
```

`docker compose config` may print a warning/error for missing environment values; fix those rather than treating successful parsing as evidence of a safe deployment.

## Backup and restore

Backups are encrypted to the configured age recipient. Keep the corresponding private identity offline and separate from the backup host. A successful backup command alone does not prove recoverability: periodically decrypt and restore into an isolated throwaway database and verify application data and ledger invariants.

The backup runner accepts `AGE_PRIVATE_KEY_PATH` only for an optional local decryptability check; do not put the private key in the compose environment or on the backup host. If unset, that check is skipped and a restore drill remains mandatory.

## Panic procedure

`scripts/panic-wipe.sh` is irreversible. It removes project secrets and volumes, but `rm`/`shred` cannot guarantee erasure from SSDs, snapshots, copy-on-write filesystems, or journaled storage. Persistent data denial depends on LUKS key destruction or an appropriate cryptographic erase performed through a separately tested manual procedure. Test only on a throwaway host.

# Production readiness and deployment sequence

## Supported browser and TLS policy

OnionShop is intended to support **Tor Browser only**. This policy cannot be reliably enforced by the server, because clients can proxy other browsers through Tor and user-agent strings are spoofable.

Configure nginx with a self-signed ECDSA P-384 TLS certificate whose exact DNS SAN is the service's v3 `.onion` hostname. The certificate key is separate from the Tor service's Ed25519 key. Tor's onion protocol provides endpoint authentication independently; the self-signed TLS key is not cryptographically derived from or authorized by that onion key.

Do **not** use public CA-issued certificates, publish certificate/SPKI hashes, or enforce SPKI pinning. Do not advise users to bypass browser certificate warnings or import a trust root. A matching SAN validates the hostname but does not establish trust in an arbitrary self-signed certificate. Tor Browser behavior must be tested on a clean profile for the exact supported release. If it warns, stop deployment and resolve the compatibility question; do not weaken the policy or train users to ignore warnings.

The repository's certificate generator creates the required self-signed ECDSA P-384 certificate and exact onion SAN, but its output has not been independently confirmed warning-free in Tor Browser. Certificate generation is not proof of browser acceptance or a production security certification.

## Required deployment order

1. Prepare and test encrypted storage, swap policy, host firewall, OS hardening, time source, and recovery on disposable infrastructure. Review firewall rules for Docker forwarding and Tor egress; never first-test remote firewall changes over the only admin path.
2. Establish and test local-console and private-onion admin access before enabling restrictive firewall rules.
3. Provision unique application/database/Redis/Monero-RPC secrets. Generate an age identity offline; only its public recipient belongs on the backup host. Keep DB and application data on encrypted storage.
4. Create the Tor v3 onion service and record its hostname. Generate the self-signed cert with that exact DNS SAN using `scripts/gen-certs.sh '<hostname>.onion'`. Verify SAN, key match, dates, and nginx-readable permissions. Then test the HTTPS site in the exact supported Tor Browser build on a clean profile. Do not proceed if it warns.
5. Review and verify every external image digest and platform, including tag-only images, against upstream registry metadata. Independently review the Monero node/wallet posture (view-only vs hot), RPC exposure, and credentials.
6. Confirm host loopback port 8080 redirects to HTTPS and 8443 serves TLS; Tor maps onion port 80 to 8080 and 443 to 8443. nginx proxies to the app over the internal Docker network (the compose reality; see AUDIT.md §1). Test health and request flow end-to-end against /healthz — the only health route registered in internal/server/router.go (/health returns 404); the nginx health location already proxies to it.
7. Test network routes and egress by service; Docker internal networks do not by themselves prove host firewall safety, DNS policy, or Tor-only node connectivity.
8. Run migrations and Go/sqlc tests on disposable data; exercise auth, checkout, invoice polling, escrow, withdrawals, messaging friction, and audit behavior.
9. Create an encrypted backup, move it off-host, and restore it with the offline age identity into an isolated environment. Verify migration state, representative records, ledger balance, and audit continuity.
10. Assign operational ownership for image/dependency updates, canary refresh/signature, secrets rotation, restore drills, and incident response. Test panic/key-destruction procedures on disposable infrastructure.

## Local validation commands

These are syntax/build checks only, not production security evidence:

```sh
sqlc generate
go test ./...
go vet ./...
bash -n scripts/*.sh install/install.sh
docker compose config --quiet
docker compose build
```


## Debian 13 quick start (development preview)
```sh
sudo ./install/install.sh --dev                  # bootstrap Docker, secrets, dev TLS, migrations, stack
sudo ./install/install.sh --onion <host>.onion   # real onion SAN certificate
./install/install.sh --check-only                # preflight, no side effects
```
The installer is idempotent and applies db/migrations/*.up.sql in order with a schema_migrations ledger. Migration 0023 begins with DROP VIEW IF EXISTS account_balances; so it applies cleanly on databases whose view still carries the pre-0009 balance_sat column name. Where /tmp is noexec (Whonix), export GOTMPDIR=$HOME/.gotmp before go test. A green preview stack is not production clearance.

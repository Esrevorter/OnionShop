# Production readiness and deployment sequence

## Current readiness statement

The application has broad feature coverage, but the repository's compose/installer configuration is not sufficient evidence for safe production deployment. In particular, `scripts/gen-certs.sh` creates a generic self-signed certificate with IP SANs, not an onion-bound certificate. Its SPKI hash does not authenticate the onion service and must not be pinned or published. A normal browser may show certificate errors. Do not bypass those warnings or operate a real service until a browser-compatible, onion-authenticated trust path is implemented and independently tested.

This document is an operator review sequence, not a claim that all controls are implemented.

## Required order

1. **Choose and test the host.** On disposable infrastructure, provision LUKS2 and encrypted/no swap as appropriate, a deny-by-default host firewall, minimal OS, time source, kernel settings, and a recovery path. Review `deploy/nftables.conf` for host-specific interfaces, UIDs, Docker forwarding, and Tor egress before loading any rules. Never first-test firewall changes over the only remote admin channel.
2. **Establish administration first.** Configure and test the Dropbear private onion and local console recovery before enabling restrictive firewall rules. Keep private admin-onion keys and SSH keys under separate backup controls.
3. **Prepare secrets and storage.** Put Postgres data, WAL, Tor service identity, and encrypted backups on encrypted storage. Generate an age identity offline and set only its public recipient in `.env` as `AGE_PUBLIC_KEY`; keep DB/Redis/session/BTCPay credentials private and mode 0600. The private age identity must not be mounted into the backup container.
4. **Resolve TLS identity.** Replace the development self-signed certificate with an onion-bound certificate and a client-supported validation method. Verify SAN, key authorization, browser behavior, rotation, and recovery. No SPKI pin should be published. If this mechanism is not available, stop before public service.
5. **Review image supply chain.** Build the app from the repository. Confirm every external image digest and platform through its upstream registry; do not assume minor tags or an unverified digest are immutable/correct. Review BTCPay's networking and wallet configuration independently.
6. **Review Compose and socket wiring.** Confirm nginx host ports are loopback-only: port 8080 serves only the redirect; port 8443 serves TLS. Tor maps onion port 80 to host `127.0.0.1:8080` and onion port 443 to host `127.0.0.1:8443`. nginx proxies to `/run/onionshop/app.sock`; the app health check must test that socket, not an unused TCP listener. Verify both published onion routes from a Tor client.
7. **Validate network policy.** Confirm no unintended public listeners or container egress. Test DNS behavior from each relevant container and host namespace. Docker's internal DNS is not itself a DNS leak to the public Internet, but service-name resolution still exists and must not be confused with a no-DNS policy. Validate BTCPay's required peer/network traffic and Tor routing; do not claim all traffic is Tor-routed without testing.
8. **Exercise application workflows.** Run migrations on a disposable database. Run `sqlc generate`, `go test ./...`, `go vet ./...`, lint, and isolated integration tests. Test login/session rotation, checkout/invoice polling, escrow transitions, withdrawals, ticket/message friction, and audit-log behavior.
9. **Prove recovery.** Create an age-encrypted backup, transfer it off-host, then restore using the offline private identity into an isolated environment. Verify schema/migration state, representative data, ledger balance invariants, and audit continuity. A backup that has not been restored is unproven.
10. **Operate and review.** Establish an owner and schedule for image/dependency updates, canary signature/refresh, access-key rotation, alert review, restore drills, and incident response. Test panic procedures on disposable infrastructure. `rm`/`shred` does not guarantee erasure from SSDs, snapshots, copy-on-write filesystems, or journals; persistent data denial depends on tested cryptographic key destruction.

## Local validation commands

These validate syntax/build behavior only; they do not prove production security:

```sh
sqlc generate
go test ./...
go vet ./...
bash -n scripts/*.sh install/install.sh
docker compose config --quiet
docker compose build
```

For production, record the exact tested host, image digests/platforms, TLS identity procedure, firewall policy, restore result, and operator sign-off. The current installer must not be treated as having performed those reviews.

# Project status and roadmap

`[x]` means the feature is present in the repository; it is not a security certification or proof of production operation.

## Application feature status

Phases 1–8 application work is implemented: foundation and server middleware; auth/RBAC; vendor/catalog; cart/checkout and BTCPay polling; escrow/ledger/disputes; support tickets/messaging; community/content. See the implementation and tests for actual coverage. The legacy entries in the initial FIXMEFIRST checklist that described these as pending were stale.

## Open engineering and production gates

- [ ] Expand high-risk escrow transition and template test coverage; complete isolated integration tests.
- [ ] Verify all external image digests and platforms; several compose images remain mutable tags.
- [ ] Keep Go toolchain pin rationale accurate; Go 1.25 is the repository's pinned CI/tooling choice, not an asserted pgx minimum.
- [ ] Review the current ProtonMail go-crypto PGP implementation and dependency maintenance/security posture.
- [ ] Implement and independently test browser-compatible onion-authenticated TLS. Current generated self-signed certificate is test-only and is not onion-bound; do not pin its SPKI.
- [ ] Verify actual Tor/host/Compose port mapping, UNIX socket health checks, service egress/DNS, and BTCPay network configuration.
- [ ] Complete disposable-host LUKS/firewall/systemd/Dropbear setup and recovery tests.
- [ ] Require backup configuration, perform an offline-key restore drill, and establish canary/incident ownership.
- [ ] Ensure installer messaging and gates cannot imply that the generic certificate or unverified host policy is production-safe.

Follow `PRODUCTION_READINESS.md` for the required sequence. A running local stack is not production clearance.

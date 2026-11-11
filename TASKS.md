# Project status and roadmap

`[x]` means an application feature exists, not that it is security-certified or production-operated.

## Application feature status

Phases 1–8 application features are implemented. The initial FIXME checklist's statements that these were pending were stale.

## Open engineering and production gates

- [ ] Expand high-risk escrow transition/template coverage and complete isolated integration tests.
- [ ] Verify external image digests/platforms; some compose images remain mutable tags.
- [ ] Keep Go toolchain rationale accurate: Go 1.25 is the repository's project pin, not an asserted pgx minimum.
- [ ] Review ProtonMail go-crypto dependency maintenance/security posture.
- [ ] Verify the self-signed ECDSA P-384 certificate has exact DNS SAN for actual v3 onion hostname; test exact supported Tor Browser version on clean profile. Never use a CA-issued cert, SPKI/hash pin, or warning bypass. Onion protocol provides onion identity; TLS key is separate.
- [ ] Verify Tor/host/Compose port mapping, UNIX socket permissions/health checks, service egress/DNS, and BTCPay networking.
- [ ] Complete disposable-host LUKS/firewall/systemd/Dropbear and recovery tests.
- [ ] Complete offline-key backup restore and establish canary/incident ownership.

See `PRODUCTION_READINESS.md`; a running local stack is not production clearance.

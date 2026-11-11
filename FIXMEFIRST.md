# OnionShop repository audit — status as of the current remediation branch

This replaces the stale original audit checklist. Its old claims about Phases 3–8 being pending were inaccurate; those application features are present. Feature presence does not establish production readiness.

## Remediated in this branch

- BTCPay's placeholder digest was replaced with a digest reference. Verify its platform and digest against registry metadata before deployment; several other images are still tag-only.
- Fixed the backup interval fallback typo and removed the guaranteed-failing `/nonexistent` private-key check. An optional decrypt check runs only when `AGE_PRIVATE_KEY_PATH` is set; keep the private identity offline and perform restore drills.
- Added SQL generation prerequisites to Make build/test/lint targets.
- Removed stale imports and corrected checkout page data rendering.
- Replaced misleading panic-wipe `shred` claims with explicit limitations and ordinary file removal; use separately tested cryptographic key destruction for persistent data denial.

## Production blockers and follow-ups

- The generated self-signed certificate is not onion-bound. Its SPKI hash is not an authentication mechanism. Production TLS identity and client-compatible rotation remain unresolved.
- Confirm Tor port 80 goes to nginx's HTTP redirect listener, Tor port 443 to TLS, and nginx's app health check traverses the actual UNIX socket.
- Several container image references are tags rather than immutable, verified digests.
- CI vulnerability scanning currently tolerates errors; establish a supported toolchain/findings policy before making it a required gate.
- Host firewall, LUKS, systemd/Dropbear, BTCPay egress, backup restore, and canary procedures require independent operator validation.

See `TASKS.md` and `PRODUCTION_READINESS.md`. Do not use this audit summary as a substitute for deployment verification.

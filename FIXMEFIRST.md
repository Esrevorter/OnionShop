# OnionShop audit status

`[x]` means application feature exists; it does not certify security or production operation. The initial audit's claims that Phases 3–8 were pending were stale.

## Remediated in this branch

- Replaced BTCPay placeholder digest (verify registry metadata/platform; other images remain tags).
- Fixed backup interval fallback and removed guaranteed-failing private-key path check; offline restore drills remain required.
- Added sqlc generation prerequisites to Make build/test/lint.
- Fixed stale imports and checkout data rendering.
- Corrected panic-wipe descriptions to avoid claims of secure overwrite.
- Aligned Tor forwarding ports with nginx listeners and Compose health checks with app UNIX socket.
- Certificate generator is being aligned to require a v3 onion hostname and create self-signed ECDSA P-384 with exact DNS SAN; TLS key remains separate from Tor Ed25519 key and no SPKI value should be published.

## Remaining gates

- Test self-signed exact-SAN certificate behavior on clean supported Tor Browser profile; do not bypass any warning.
- Independently verify host LUKS/firewall/systemd/Dropbear, egress, all image digests, BTCPay config, and an offline-key restore.

Publicly CA-issued certificates and SPKI/hash pinning are not OnionShop policy.

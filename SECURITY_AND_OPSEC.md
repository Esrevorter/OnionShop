# Security & Operational Security (OpSec) Guidelines

> **Threat model:** assume adversarial Tor relays, possible host seizure, and that operator network identity is sensitive. Controls are defense-in-depth; a checklist or successful compose start is not a security audit.

## 1. Onion service and application boundaries

- Do not log, store, or use client IP addresses. Do not expose public app listeners. nginx should be reachable only via host loopback and should proxy to the app over the shared UNIX socket.
- Tor service identity must be protected on encrypted storage and backed up only in encrypted form. Keep public storefront and private admin onion identities separate.
- Rate limits must not key on client IP. Use authenticated identities where available and CAPTCHA/honeypot friction on pre-auth actions.
- Use UTC and minimize logs; never ship sensitive logs off-host.

## 2. TLS identity: implementation limitation

The intended design is onion-authenticated TLS, without SPKI pins. The current `scripts/gen-certs.sh` does **not** implement that design: it creates a generic self-signed certificate with loopback IP SANs and no onion-key authorization. It is for local testing only. It does not authenticate a `.onion` hostname. Do not publish its SPKI value or tell users to bypass certificate warnings.

Before production, implement and independently verify an onion-authenticated certificate chain and a supported client validation/rotation path, or select another reviewed, browser-compatible trust design. Until then this is a production blocker. Onion service encryption does not make the generic certificate onion-bound.

## 3. Network and host controls

- Review nginx, Tor, Docker, and host-firewall routes together. Host-exposed ports should bind to `127.0.0.1`; onion port 80 should reach nginx's HTTP redirect listener and onion port 443 its TLS listener.
- The app/nginx hop is a UNIX socket. Health checks must test the actual socket/proxy path rather than assume the app accepts TCP.
- Docker internal networks prevent direct external routing for attached containers but do not prove the host firewall or other networks are safe. Audit DNS behavior and egress per service, especially BTCPay's peer connectivity.
- Apply LUKS2, swap, nftables, systemd sandboxing, and Dropbear private-onion administration through host-specific reviewed procedures. Test recovery before enforcing firewall rules remotely.
- Pin external container images by verified digest/platform. Tags are mutable. Review upstream metadata before updates.

## 4. Data, payments, and recovery

- Database secrets and application secrets must not be committed or baked into images. Use unique credentials and least privilege; verify actual grants and Redis commands against production code.
- Payment status is polled; do not add inbound BTCPay webhooks. Ledger entries must be append-only and movements transactional/idempotent. Verify financial workflows and invariants with tests.
- Back up with `age` to an offline-generated public recipient. The private age identity stays offline and away from the backup host. Run restore drills and verify ledger and audit continuity.
- `rm` and `shred` are not reliable secure erase on SSDs, snapshots, copy-on-write or journaled filesystems. Persistent data denial depends on tested cryptographic key destruction/erase; panic procedures must say exactly what they do and do not erase.

## 5. Production readiness

`PRODUCTION_READINESS.md` is the authoritative operator review sequence. Infrastructure files and this document describe intended controls, not evidence that a production host meets them. Do not label the deployment production-ready until the TLS identity blocker, host/network controls, backup restore, and service integration have been independently tested.

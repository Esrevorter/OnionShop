# Security & Operational Security (OpSec) Guidelines

> **Threat model:** assume adversarial Tor relays, possible host seizure, and sensitive operator network identity. A checklist is not a security audit.

## 1. Onion service, browser, and TLS policy

- Do not log, store, or use client IP addresses. Keep public app listeners off routable interfaces; nginx should be host-loopback-only and proxy to app via UNIX socket.
- Protect Tor v3 identity on encrypted storage; keep public storefront and private admin onion identities separate.
- Rate limits must not use client IP. Use authenticated identities and CAPTCHA/honeypot friction as appropriate.
- Use UTC, minimize logs, and do not ship sensitive logs off-host.
- **Supported browser:** Tor Browser only. This is a project policy, not an enforceable client restriction; other browsers can be proxied and user-agent strings spoofed.
- **Certificate format:** self-signed ECDSA P-384 TLS leaf, exact DNS SAN equal to the service's v3 `.onion` hostname. Generate with `scripts/gen-certs.sh <hostname>.onion` after onion creation. Use the private key only at nginx.
- The TLS public/private key is separate and random, not the Tor Ed25519 key and not cryptographically onion-key-bound. Onion protocol authenticates the endpoint independently. SAN matching validates hostname identity under certificate rules, but does not make an arbitrary self-signed certificate trusted.
- **Never use public CA-issued certificates for this project. Never publish or enforce SPKI/certificate-hash pins.** No CA trust and no brittle pins are part of OnionShop policy.
- Never advise users to bypass certificate warnings or import an unverified trust root. Test the exact supported Tor Browser version/profile. If it displays a certificate warning, do not deploy until the Tor Browser behavior is understood and a compatible trust path meets policy.
- Do not enable HSTS for this deployment; retain safe recovery/rotation options.

## 2. Network and host controls

- Review nginx, Tor, Docker, and host firewall together. Tor port 80 maps to nginx redirect listener 8080; onion port 443 maps to TLS listener 8443.
- App/nginx uses the shared UNIX socket; health checks should exercise the actual proxy/socket route.
- Docker internal networks do not by themselves prove host firewall safety, DNS policy, or Tor-only egress. Audit each service, especially BTCPay.
- Apply LUKS2, swap, nftables, systemd, and private-onion Dropbear through host-specific reviewed procedures and test recovery first.
- Verify external container image digest/platform against upstream metadata.

## 3. Data, payments, and recovery

- Do not commit or bake secrets. Verify database grants and Redis policy against actual code/config.
- Poll BTCPay; do not add inbound webhooks. Verify ledger transactions/idempotency and financial workflows.
- Encrypt backups with age to an offline-generated public recipient; keep private identity offline and run restore drills.
- `rm`/`shred` are not reliable erasure on SSDs, snapshots, copy-on-write or journaled filesystems. Persistent denial requires tested cryptographic key destruction.

## 4. Production readiness

Follow `PRODUCTION_READINESS.md`. Production requires actual Tor Browser TLS behavior testing, host/network verification, integration checks, and successful backup restoration; docs do not substitute for evidence.

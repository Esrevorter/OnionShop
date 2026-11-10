# Project Tasks & Roadmap

Status legend: `[x]` done and committed; `[ ]` not started.
(Previous revision incorrectly marked Phase 1 item 1 as done — no Go module existed in the
repository. Corrected 2026-10-31; documentation contradictions flagged during that review
were also fixed: Django-style CSRF syntax, `Secure` cookie requirement, HSTS, and BTCPay
webhooks. Superseded 2026-11-01 by the hardened OpSec baseline (SECURITY_AND_OPSEC.md):
nginx reverse proxy + self-signed SPKI-pinned TLS on the onion is now the design, so cookies
DO carry `Secure` and CSP includes `upgrade-insecure-requests`; browser-facing HSTS remains
prohibited. OpenSSH is replaced by Dropbear over a private onion; tmpfs/LUKS2/default-deny
egress are mandatory.)

## Phase 1: Foundation
- [x] Initialize Go module (`go.mod`, `cmd/onionshop/main.go`) and directory structure
      (`internal/{server,store,cron}`, `web/templates`, `db/migrations`).
- [x] Write `docker-compose.yml` (Postgres, Redis with `maxmemory-policy noeviction` + persistence
      OFF, nginx reverse proxy) — hardened revision done 2026
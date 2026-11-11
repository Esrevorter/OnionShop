# System Architecture

## 1. Request flow
1. User (supported browser: Tor Browser) -> Tor network -> Tor daemon hosting the v3 onion service.
2. Tor forwards onion port 80 to host loopback `127.0.0.1:8080` (nginx redirects to HTTPS), and onion port 443 to `127.0.0.1:8443` (nginx TLS).
3. nginx presents a self-signed ECDSA P-384 certificate with the exact service `.onion` hostname in DNS SAN. This is a TLS key separate from the Tor Ed25519 service identity; the Tor onion protocol authenticates the endpoint independently.
4. nginx proxies to the Go app over the shared UNIX socket (`/run/onionshop/app.sock`).
5. Go renders HTML via `html/template`; responses return through nginx and Tor.

## 2. Component interaction

- **nginx:** loopback-only reverse proxy, TLS termination, static assets, and UNIX-socket proxy to Go. The product policy is Tor Browser only with self-signed exact-onion-SAN TLS; no publicly CA-issued certs or SPKI pinning. Browser warning behavior is version-sensitive and must be tested before deployment.
- **Go backend:** chi routing and server-rendered business logic. Compose uses a UNIX socket; no public host interface.
- **PostgreSQL:** persistent application data on encrypted storage; never put DB/WAL on tmpfs.
- **Redis:** ephemeral sessions/rate/cart data with persistence disabled in current configuration; verify fallback behavior.
- **BTCPay:** application polls invoice status; network/wallet policy requires independent review.
- **Dropbear:** intended host-level admin over a separate private onion, outside Compose.

## 3. Security headers

Go and nginx emit security headers. No HSTS or TLS pinning is configured. A certificate SAN match and Tor onion endpoint authentication are distinct checks; a self-signed certificate may still trigger browser certificate UI. Test Tor Browser; never advise bypassing warnings.

## 4. Escrow, ledger, support, and community

Escrow and ledger operations are intended to be transactional/idempotent; verify their invariants and recovery through tests. Support, messaging, and community pages are server-rendered and use the anti-abuse/privacy controls documented in the code and `SECURITY_AND_OPSEC.md`.

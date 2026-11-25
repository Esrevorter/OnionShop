# System Architecture

## 1. Request flow
1. User (supported browser: Tor Browser) -> Tor network -> Whonix-Gateway (Tor daemon hosting the v3 onion service).
2. Whonix-Gateway forwards onion port 80 to the Whonix-Workstation (AppVM) loopback `127.0.0.1:8080` (nginx redirects to HTTPS), and onion port 443 to `127.0.0.1:8443` (nginx TLS).
3. nginx presents a self-signed ECDSA P-384 certificate with the exact service `.onion` hostname in DNS SAN. This is a TLS key separate from the Tor Ed25519 service identity; the Tor onion protocol authenticates the endpoint independently.
4. nginx proxies to the Go app over the shared UNIX socket (`/run/onionshop/app.sock`).
5. Go renders HTML via `html/template`; responses return through nginx, the AppVM, the Whonix-Gateway, and the Tor network.

## 2. Component interaction

- **Whonix-Gateway:** Dedicated VM running the Tor daemon. Provides strict network isolation and forces all AppVM traffic through the Tor network. Hosts the v3 onion service configuration.
- **Whonix-Workstation (AppVM):** Dedicated VM hosting the Docker Compose stack (`app`, `postgres`, `nginx`, `redis`, `monerod`, `monero-wallet-rpc`, `backup`). It has **no direct physical network interface**; all egress and ingress is routed exclusively through the Whonix-Gateway.
- **nginx:** loopback-only reverse proxy, TLS termination, static assets, and UNIX-socket proxy to Go. The product policy is Tor Browser only with self-signed exact-onion-SAN TLS; no publicly CA-issued certs or SPKI pinning.
- **Go backend:** chi routing and server-rendered business logic. Compose uses a UNIX socket; no public host interface.
- **PostgreSQL:** persistent application data on encrypted storage; never put DB/WAL on tmpfs.
- **Redis:** ephemeral sessions/rate/cart data with persistence disabled in current configuration; verify fallback behavior.
- **Monero settlement (implemented):** `monerod` (own node) plus `monero-wallet-rpc`, internal network only, no published ports. The app speaks JSON-RPC directly (`create_address` per order, `get_transfers` for observation). **No MoneroPay daemon** - this is the in-house client option anticipated in TASKS.md 10.9. Detection is **polling only**, never webhooks.
- **Payment matching:** a transfer confirms an order only when it lands on that order's own subaddress, equals the invoice amount exactly, and meets the confirmation threshold. Overpayment sets `payment_flag='over'` for manual review and never auto-releases; underpayment records the delta and requests the remainder.
- **Dropbear:** intended host-level admin over a separate private onion, ideally hosted on a distinct, minimal AppVM or the Gateway with strict access controls.

## 3. Security headers

Go and nginx emit security headers. No HSTS or TLS pinning is configured. A certificate SAN match and Tor onion endpoint authentication are distinct checks; a self-signed certificate may still trigger browser certificate UI. Test Tor Browser; never advise bypassing warnings.

## 4. Escrow, ledger, support, and community

Escrow transitions route through the pure state machine (`escrow.go`) with the DB CHECK as a second line of defence; every mutating UPDATE carries a status guard. Ledger movements are double-entry, append-only and idempotent (`release:<order>`, `refund:<order>`, `withdraw:<id>`). Refunds and payouts broadcast on-chain and persist their tx hash. Buyer delivery details are stored only as PGP ciphertext addressed to the vendor's public key.

### Automated timers (hourly cron)
- 12 h unpaid: `cancelled`, buyer notified.
- 3 days in escrow without shipment: `refunded` on-chain to the buyer's refund address.
- Shipped plus shipping estimate plus 3-day grace: `released`, blocked while a dispute is open.

### Order lifecycle notifications
Every event (created, paid, shipped, extended, released, refunded, disputed, cancelled, over/underpaid) posts an audience-targeted message into the single buyer-vendor thread, so each party sees only what is addressed to them; unread counts drive the red header badge. Support, messaging, and community pages are server-rendered and use the anti-abuse/privacy controls documented in the code and `SECURITY_AND_OPSEC.md`.

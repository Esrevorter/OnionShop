# System Architecture

## 1. Request Flow
1. User (Tor Browser) -> Tor Network -> Tor Daemon (v3 Hidden Service).
2. Tor Daemon -> forwards to `127.0.0.1:8080` (Go Web Server).
3. Go Web Server -> renders HTML via `html/template` -> sends back through Tor.

## 2. Component Interaction
- **Go Backend:** Handles routing (`chi`), business logic, and rendering.
- **PostgreSQL:** Stores persistent data (Users, Products, Orders, Ledger).
- **Redis:** Stores ephemeral data (User Sessions, Rate Limit counters, Shopping Carts).
- **BTCPay Server:** Runs as a separate service. Go backend communicates via REST API to create invoices and listens for Webhooks to update order statuses.

## 3. Escrow State Machine
- `awaiting_payment` -> (BTCPay Webhook) -> `in_escrow`
- `in_escrow` -> (Vendor marks shipped) -> `shipped`
- `shipped` -> (Customer confirms OR 14-day cron) -> `released` (Credits Vendor Ledger)
- `shipped` -> (Dispute opened) -> `disputed` -> (Mod resolves) -> `released` OR `refunded`

# Project Tasks & Roadmap

## Phase 1: Foundation (Current)
- [x] Initialize Go module and directory structure.
- [ ] Write `docker-compose.yml` (Postgres, Redis).
- [ ] Write SQL migrations for the full schema.
- [ ] Set up DB connection pool and Redis client.

## Phase 2: Core Server & Security
- [ ] Setup `chi` router and basic HTTP server.
- [ ] Implement Security Headers middleware (CSP, HSTS, etc.).
- [ ] Implement Session management (`scs`) with Redis.
- [ ] Implement CSRF protection middleware.
- [ ] Implement Redis-based Rate Limiting middleware.

## Phase 3: Auth & RBAC
- [ ] User Registration & Login (Argon2 passwords).
- [ ] RBAC Middleware (Customer, Vendor, Mod, Admin).
- [ ] HTML Templates for Auth (with Math CAPTCHA & Honeypot).

## Phase 4: Catalog & Vendors
- [ ] Vendor registration and Admin approval flow.
- [ ] Product CRUD for vendors.
- [ ] Public storefront, search, and category browsing.

## Phase 5: Cart, Checkout & BTCPay
- [ ] Server-side session cart.
- [ ] BTCPay API client and Webhook receiver.
- [ ] Checkout flow and QR code generation.

## Phase 6: Escrow, Ledger & Disputes
- [ ] Ledger service (Credit/Debit with Postgres Transactions).
- [ ] Escrow state machine and auto-finalization cron job.
- [ ] Dispute handlers and chat interface.
- [ ] Vendor withdrawal requests and Admin approval.

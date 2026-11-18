# OnionShop UI/UX Progress Tracker

> **Last updated:** 2026-11-18
> **Status:** Pre-Phase 10.2 — core auth and catalog working; admin, security, and user features need substantial work.

---

## 1. Current State Summary

| Area | Status | Notes |
|------|--------|-------|
| Authentication (login/register) | ✅ Working | CSRF + CAPTCHA + honeypot |
| Session management | ✅ Working | Redis + Postgres fallback |
| Catalog / storefront | ✅ Working | Home, search, category, product, shop pages |
| Vendor application | ⚠️ Partial | Form + backend exist; approval queue UI is minimal |
| Admin vendor queue | ⚠️ Partial | Route works; no dashboard, no user management |
| Wallet / Orders / Disputes / Tickets | 🟡 Shell only | Templates render; no real logic or data |
| Checkout flow | ❌ Missing | Template exists; no cart, no payment integration |
| 2FA | ❌ Missing | No TOTP/WebAuthn implementation |
| Raffle system | ❌ Missing | Not scoped yet |
| User banning/deletion | ❌ Missing | No admin interface |
| Direct messaging | ❌ Missing | Schema may exist; no UI |
| Member/vendor administration | ❌ Missing | No dashboard |

---

## 2. What's Actually Working

- [x] User registration with CAPTCHA + honeypot
- [x] Login with session rotation
- [x] Logout (fixed session-destroy panic)
- [x] Anti-phishing personal phrase
- [x] PGP public key upload/clear
- [x] Catalog browsing (home, search, category, product, vendor shop)
- [x] Vendor application form
- [x] Admin vendor approval queue (backend functional)
- [x] Navigation header with authenticated-state detection
- [x] Purple SVG brand mark + favicon
- [x] Red "Create account" CTA in nav
- [x] Whonix-compatible nginx binding (`NGINX_BIND_IP`)
- [x] Monero node (`monerod`) service in compose

---

## 3. What's Partially Implemented (Backend Exists, UI Incomplete)

- [ ] **Vendor product dashboard** — handlers exist; no UI to create/edit/remove products
- [ ] **Order system** — database schema exists; no checkout flow, no order history UI
- [ ] **Dispute system** — schema exists; no UI to open/resolve disputes
- [ ] **Support tickets** — schema exists; minimal UI, no admin view
- [ ] **Reviews** — backend works; UI exists but untested end-to-end
- [ ] **Withdrawals** — template exists; no backend logic or admin approval flow
- [ ] **Ledger / balance tracking** — backend scaffolding exists; no UI

---

## 4. What's Not Implemented At All

- [ ] **2FA (TOTP/WebAuthn)** — no implementation
- [ ] **Raffle system** — not scoped
- [ ] **Comprehensive admin dashboard** — no overview of users/vendors/orders/disputes
- [ ] **User management** — no list, ban, suspend, or delete interface
- [ ] **Vendor management** — no product oversight, no messaging, no analytics
- [ ] **Direct messaging** — user-to-user and user-to-vendor
- [ ] **Login notifications** — no alert on new device/location
- [ ] **Session management UI** — no view/kill active sessions
- [ ] **Email notifications** — not scoped (and may conflict with OpSec)
- [ ] **Advanced search/filters** — basic search exists; no facets
- [ ] **Vendor analytics dashboard** — sales, views, conversion

---

## 5. Phased Roadmap

### Phase A — Core Admin (Priority 1)
**Goal:** Operator can manage the marketplace without touching the database directly.

- [ ] **A.1 Admin Dashboard** — overview cards: user count, pending vendors, open disputes, recent orders
- [ ] **A.2 User Management** — list users, search, view details, ban/suspend, reset password
- [ ] **A.3 Vendor Management** — approve/reject queue, view products, message vendors, revoke approval
- [ ] **A.4 Order Management** — list all orders, filter by status, view details, force-cancel
- [ ] **A.5 Dispute Management** — list open disputes, view evidence, resolve (refund/escrow release)
- [ ] **A.6 Support Ticket Admin** — list tickets, respond, close, escalate

### Phase B — Security Hardening (Priority 2)
**Goal:** Protect accounts against takeover and credential stuffing.

- [ ] **B.1 TOTP 2FA** — QR code setup, backup codes, login prompt
- [ ] **B.2 Login notifications** — flag new session/device in account page
- [ ] **B.3 Session management UI** — list active sessions, kill individual or all
- [ ] **B.4 Rate limit tuning** — per-endpoint tuning for login/register/2FA
- [ ] **B.5 Audit log viewer** — admin can see who did what (migration 0003 exists)

### Phase C — User Features (Priority 3)
**Goal:** Complete the buyer and vendor experience.

- [ ] **C.1 Full checkout flow** — cart → address → payment → confirmation
- [ ] **C.2 Order history** — customer sees their orders with status
- [ ] **C.3 Order detail** — tracking, vendor messages, dispute button
- [ ] **C.4 Dispute opening** — customer can open dispute from order page
- [ ] **C.5 Messaging system** — user↔vendor threads (schema exists)
- [ ] **C.6 Raffle system** — if core to marketplace model
- [ ] **C.7 Vendor product dashboard** — CRUD UI for listings
- [ ] **C.8 Vendor analytics** — sales, views, conversion rate

### Phase D — Polish & Operations (Priority 4)
**Goal:** Production-ready operator experience.

- [ ] **D.1 Withdrawal flow** — vendor requests, admin approves, Monero payout
- [ ] **D.2 Ledger/balance UI** — real balance display, transaction history
- [ ] **D.3 Advanced search** — facets, sorting, saved searches
- [ ] **D.4 Vendor analytics dashboard** — charts, exports
- [ ] **D.5 Backup/restore drill documentation** — tested procedure
- [ ] **D.6 OpSec hardening pass** — review all new surfaces against `SECURITY_AND_OPSEC.md`

---

## 6. Immediate Next Steps

1. **Phase 10.2 — Monero Wallet RPC** (view-only wallet, key generation script)
2. **Phase A.1 — Admin Dashboard** (once Monero integration is stable)
3. **Phase B.1 — TOTP 2FA** (security before scaling user features)

---

## 7. Notes & Decisions

- **No email notifications:** Conflicts with OpSec (clearnet egress). In-app notifications only.
- **No JavaScript:** CSP is `script-src 'none'`. All UI must be server-rendered HTML.
- **Tor Browser only:** No HSTS, no SPKI pins, no CA-issued certs.
- **View-only Monero wallet:** Spend key never touches the server. Payouts require manual signing.
- **Whonix deployment:** HS terminates on Gateway; app on Workstation. `NGINX_BIND_IP` controls binding.

---

## 8. Changelog

| Date | Change |
|------|--------|
| 2026-11-18 | Initial document created. Captures pre-Phase 10.2 state. |

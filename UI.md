# OnionShop UI/UX Progress Tracker

> **Last updated:** 2026-11-21
> **Status:** Post fiat/XMR migration — the full commerce loop (browse → cart →
> checkout → escrow payment instructions → messaging → vendor analytics) is
> working. Admin and security-hardening phases are next.

---

## 1. Current State Summary

| Area | Status | Notes |
|------|--------|-------|
| Authentication (login/register) | ✅ Working | CSRF + CAPTCHA + honeypot |
| Session management | ✅ Working | Redis + Postgres fallback |
| Catalog / storefront | ✅ Working | Home, search, category, product, shop pages; currency-aware pricing |
| Vendor application | ⚠️ Partial | Form + backend exist; approval queue UI is minimal |
| Admin vendor queue | ⚠️ Partial | Route works; no dashboard, no user management |
| Wallet | ⚠️ Partial | XMR withdrawal request + admin review; balance placeholder pending ledger |
| Orders | ✅ Working | History with status, items, shipping, XMR payment instructions |
| Disputes / Tickets | 🟡 Shell only | Templates render; no real logic or data |
| Checkout flow | ✅ Working | Cart → per-vendor shipping → locked XMR settlement per order |
| Cart | ✅ Working | Per-vendor groups, qty update/remove, shipping previews |
| 2FA | ❌ Missing | No TOTP/WebAuthn implementation |
| Raffle system | ❌ Missing | Not scoped yet |
| User banning/deletion | ❌ Missing | No admin interface |
| Direct messaging | ✅ Working | Order-linked buyer↔vendor threads, inbox, unread badges |
| Member/vendor administration | ❌ Missing | No dashboard |
| Vendor analytics | ✅ Basic | /vendor/stats dashboard + privacy-safe aggregate view counters |

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
- [x] Fiat-pegged pricing: vendors enter USD (decimal), stored as integer cents
- [x] Viewer-currency display: USD/EUR/AUD pills in session bar (active = red),
      persisted server-side via `POST /currency/set`; no JavaScript involved
- [x] Money helpers: `fmtFiat` (viewer currency, nil-oracle safe), `fmtUSD`
      (base), `fmtXMR` (12-decimal piconero formatting)
- [x] Vendor listing CRUD UI: create/edit/publish/remove (CSRF + role gated)
- [x] Vendor shipping profiles: CRUD at /vendor/shipping (title, description,
      USD price, speed, active toggle)
- [x] Cart: per-vendor grouping, qty update/remove, subtotals,
      "shipping from X" previews, currency-aware summary
- [x] Checkout: per-vendor shipping radios, delivery instructions, CAPTCHA
      friction, per-vendor order splitting, shipping folded into totals
- [x] XMR settlement: per-order locked piconero amount, Monero subaddress and
      payment reference stored on the order; mock provider by default,
      wallet-rpc provider when `MONERO_WALLET_RPC_URL` is set
- [x] Order history: status badges, vendor shop names, shipping method, item
      lines, totals in viewer currency
- [x] Escrow payment instructions: exact XMR amount + address + reference for
      awaiting_payment orders
- [x] Messaging: inbox with unread badges, thread view, reply form; threads are
      pair-unique and order-linked; participants derived from the order
- [x] Vendor statistics: orders by status, gross sales, units sold, distinct /
      repeat customers, avg order value, released vs in-escrow, rating,
      listing health, top products, recent orders, 30-day view chart
- [x] Privacy-safe analytics: `site_stats(day, vendor_id, metric, count)`
      aggregate counters only — no IPs, no user identifiers, no cookies
- [x] Migrations 0008 (XMR-native columns), 0009 (fiat pricing), 0010
      (shipping options); live-schema repair applied; `schema_migrations`
      aligned to version 10

---

## 3. What's Partially Implemented (Backend Exists, UI Incomplete)

- [ ] **Vendor application / approval queue** — backend functional; queue UI minimal
- [ ] **Dispute system** — schema exists; no UI to open/resolve disputes
- [ ] **Support tickets** — schema exists; minimal UI, no admin view
- [ ] **Reviews** — backend works; UI exists but untested end-to-end
- [ ] **Withdrawals** — request form (XMR) + admin review route exist; Monero
      payout is manual; vendor balance still a placeholder
- [ ] **Ledger / balance tracking** — double-entry tables exist; no UI

---

## 4. What's Not Implemented At All

- [ ] **Buyer-side order cancellation + 3-day auto-cancel** — designed and
      scripted; deployment intentionally paused by operator (2026-11-21)
- [ ] **Branded internal-error page** — `writeGenericError` still emits plain text
- [ ] **Live exchange-rate feed** — oracle serves seeded defaults over Tor
- [ ] **Product-page "shipping from X" hint**
- [ ] **2FA (TOTP/WebAuthn)** — no implementation
- [ ] **Raffle system** — not scoped
- [ ] **Comprehensive admin dashboard** — no overview of users/vendors/orders/disputes
- [ ] **User management** — no list, ban, suspend, or delete interface
- [ ] **Vendor management oversight** — no product moderation or revoke flow
- [ ] **Login notifications** — no alert on new device/location
- [ ] **Session management UI** — no view/kill active sessions
- [ ] **Email notifications** — not scoped (and conflicts with OpSec)
- [ ] **Advanced search/filters** — basic search exists; no facets

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

- [x] **C.1 Full checkout flow** — cart → shipping selection → locked XMR → confirmation
- [x] **C.2 Order history** — customer sees their orders with status + payment instructions
- [ ] **C.3 Order detail** — tracking, vendor messages, dispute button (messages link exists)
- [ ] **C.4 Dispute opening** — customer can open dispute from order page
- [x] **C.5 Messaging system** — user↔vendor threads with unread badges
- [ ] **C.6 Raffle system** — if core to marketplace model
- [x] **C.7 Vendor product dashboard** — CRUD UI for listings incl. publish/remove
- [x] **C.8 Vendor analytics** — sales, customers, ratings, views (basic dashboard)
- [ ] **C.9 Order cancellation** — buyer cancel for unpaid orders + 3-day auto-cancel (paused)

### Phase D — Polish & Operations (Priority 4)
**Goal:** Production-ready operator experience.

- [ ] **D.1 Withdrawal flow** — vendor requests + admin review exist; Monero payout manual
- [ ] **D.2 Ledger/balance UI** — real balance display, transaction history
- [ ] **D.3 Advanced search** — facets, sorting, saved searches
- [ ] **D.4 Vendor analytics dashboard** — charts, exports (basic chart exists)
- [ ] **D.5 Backup/restore drill documentation** — tested procedure
- [ ] **D.6 OpSec hardening pass** — review all new surfaces against `SECURITY_AND_OPSEC.md`

---

## 6. Immediate Next Steps

1. **Order cancellation** — buyer cancel for `awaiting_payment` orders + hourly
   auto-cancel after 3 days (script prepared; resume when operator ready)
2. **Branded error page** — replace plain-text `writeGenericError` output
3. **Phase A.1 — Admin Dashboard** (operator visibility before scaling users)
4. **Phase B.1 — TOTP 2FA** (security before scaling user features)
5. **Live FX feed + ledger-backed wallet balance** (replace seeded oracle defaults)

---

## 7. Notes & Decisions

- **No email notifications:** Conflicts with OpSec (clearnet egress). In-app notifications only.
- **No JavaScript:** CSP is `script-src 'none'`. All UI is server-rendered HTML;
  interactive controls are native forms (e.g., currency pills are submit
  buttons). Consequence: no `confirm()` dialogs — destructive actions execute
  on click by design.
- **Money model:** prices and totals are integer USD cents end-to-end
  (`price_usd_cents`, `total_usd_cents`, `unit_price_usd_cents`); XMR settlement
  amounts are integer piconero (`xmr_amount_piconero`), locked per vendor order
  at checkout. Display always goes through `fmtFiat`/`fmtUSD`/`fmtXMR`; the
  nil-oracle fallback formats USD and never renders a bare integer.
- **Currency selection:** server-side session state (`PreferredCurrency`);
  selector posts to `/currency/set`; active pill rendered red for visibility.
- **Static asset cache-busting:** stylesheet links carry `?v=YYYYMMDD`; bump the
  tag in the same commit as any CSS change (Tor Browser caches aggressively).
- **Friction policy:** CAPTCHA challenges only where a challenge is minted on
  the GET that renders the form. Low-risk POSTs (cart actions, publish/remove,
  shipping toggles, message replies) use CSRF + honeypot only — an un-mintable
  challenge silently swallows submissions (bug class fixed twice).
- **Privacy-safe analytics:** `site_stats` stores daily aggregate counters per
  vendor and metric only. No per-visitor data ever persists.
- **Tor Browser only:** No HSTS, no SPKI pins, no CA-issued certs.
- **View-only Monero wallet:** Spend key never touches the server. Payouts require manual signing.
- **Whonix deployment:** HS terminates on Gateway; app on Workstation. `NGINX_BIND_IP` controls binding.

---

## 8. Changelog

| Date | Change |
|------|--------|
| 2026-11-18 | Initial document created. Captures pre-Phase 10.2 state. |
| 2026-11-20 | Fiat/XMR migration landed: migrations 0008–0010, currency selector, fmtFiat pricing everywhere, shipping commerce, working cart/checkout/orders, order-linked messaging, vendor stats dashboard; live-schema repair + schema_migrations aligned to 10. |
| 2026-11-21 | UI.md refreshed to post-migration state. Order cancellation designed but deployment paused by operator decision. |

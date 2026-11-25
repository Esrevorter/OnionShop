# OnionShop UI/UX Progress Tracker
> **Last updated:** 2026-11-24
> **Status:** Full commerce loop live on a private Monero testnet: browse, cart,
> checkout (PGP-encrypted delivery + refund address), exact-amount XMR payment,
> escrow, ship, release/dispute, ledger wallet, on-chain withdrawal.
> Help Center and grouped navigation shipped. Production gates remain open.

## 1. Current state
| Area | Status | Notes |
|------|--------|-------|
| Auth + 2FA (TOTP) | done | CSRF + CAPTCHA + honeypot, backup codes, session rotation |
| Anti-phishing phrase | done | Header on every authenticated page |
| Admin suite | done | Dashboard, users, sessions, vendors, orders, disputes, tickets, withdrawals, audit, settings |
| Catalog / search / shop | done | Currency-aware pricing, real dispatch ETA from order history |
| Cart / checkout | done | Per-vendor shipping, authoritative price/stock re-validation, atomic stock decrement + rollback |
| Delivery privacy | done | Encrypted to vendor PGP key; server never decrypts |
| Payments (XMR) | done | Per-order subaddress, exact-amount match, over/under flags, 12 h expiry |
| Escrow | done | Pure state machine + DB CHECK; cancel/ship/extend/release guarded |
| Disputes / tickets | done | Open, moderate, resolve to release or on-chain refund |
| Wallet / withdrawals | done | Ledger balance, pending, history; paid withdrawal broadcasts on-chain |
| Messaging | done | Unified lifecycle notifications, audience targeting, unread badge, hide/restore/delete |
| Reviews | done | Shipped/released orders inside the edit window |
| Vendor onboarding | done | Apply, registration fee (admin-set USD, paid XMR), approval; fee gate enforced |
| Vendor analytics | done | Sales, customers, ratings, listing health, privacy-safe counters |
| Help Center | done | /help, /help/buyers, /help/vendors, /help/admins (role-gated) |
| Navigation | done | Grouped no-JS details menus: Selling / Admin / Account |
| Raffle system | not scoped | |
| Advanced search facets | open | Basic search only |

## 2. UX decisions (authoritative)
- **No JavaScript.** CSP script-src 'none'; native forms, details menus, user-select:all copy blocks.
- **Money display:** native XMR trimmed to 6 decimals with fiat context. Invoices are rounded at
  creation so displayed == payable. Integer cents/piconero end to end; exact decimal parsing
  (parseDecimalUnits / parseDecimalUnitsAllowZero), no float in the money path.
- **Friction policy:** CAPTCHA only where the GET mints a challenge; low-risk POSTs use CSRF +
  honeypot. Verified per form by audit; an un-mintable challenge silently swallows submissions.
- **Destructive mailbox actions** render solid red with white text for contrast.
- **Status badges** are per state (statusBadge), not binary green/muted.
- **Cache busting:** bump main.css?v= in the same commit as any CSS change.

## 3. Remaining UX work
- [ ] Advanced search facets and sorting
- [ ] Raffle system, if core to the model
- [ ] Server-rendered QR for a monero: URI (in-process PNG, no external service)
- [ ] Explicit check-payment-status POST affordance on the order page

## 4. Changelog
| Date | Change |
|------|--------|
| 2026-11-18 | Initial tracker |
| 2026-11-20 | Fiat/XMR migration, cart/checkout/orders, messaging, vendor stats |
| 2026-11-24 | Refreshed to shipped reality: 2FA, admin suite, disputes/tickets, ledger wallet and on-chain withdrawals, cancellation/refunds, unified notifications, Help Center, grouped nav, native XMR display, float-free money path |

# AI Agent Rules & Constraints

## 1. Absolute prohibitions
- **No JavaScript:** Do not write, suggest, or import JavaScript, TypeScript, React, Vue, Alpine, or HTMX.
- **No CDNs:** Serve CSS, fonts, and images locally.
- **No IP tracking:** Never log, store, or use the client IP address.

## 2. Go and backend standards
- Go toolchain is pinned by `go.mod` and CI. Go 1.25.x is a project/tooling choice; do not claim individual dependencies require it unless verified from module metadata.
- Keep routing on chi and explicit method/middleware checks.
- Use `html/template`, Argon2id, and pgx/sqlc; do not use GORM.
- Handle errors explicitly and use contexts for database/external API calls.

## 3. Frontend and UI standards
- Use semantic HTML and standard POST forms for state changes, with CSRF hidden inputs.
- Use CSS `:checked` or `<details>/<summary>` for interactivity.
- CAPTCHA is local, single-use, session-bound, and short-lived. Do not use external CAPTCHA/CDN services. Registration, ticket, and message forms use the documented honeypot.
- Ticket/message content is escaped plain text; user URLs stay inert; unread badges render server-side.

## 4. Security and Tor operations
- Never log passwords, private PGP keys, or sensitive user data.
- Cookies must be `HttpOnly`, `Secure`, and `SameSite=Strict`; do not use HSTS without a tested recovery path.
- Sessions require at least 128 bits CSPRNG entropy, rotation on login/privilege change, and idle/absolute expiry.
- Store public PGP keys only; private key material must never touch the server.
- Bind the app to a UNIX socket or loopback unless a reviewed contained namespace specifically requires otherwise. Never bind a public host interface.
- TLS policy: self-signed ECDSA P-384 with the exact onion DNS SAN. **No SPKI/certificate pinning, no CA-issued certificates** (authoritative: SECURITY_AND_OPSEC.md section 1).


## Current implementation contract (authoritative)
- Settlement: in-house `monero-wallet-rpc` JSON-RPC client. **Do not add MoneroPay or
  BTCPay.** Polling only; never introduce an inbound webhook or callback route.
- Money: integer cents/piconero only. Parse decimal input with `parseDecimalUnits`
  (or `parseDecimalUnitsAllowZero` where 0 is meaningful). **Never** `ParseFloat` in a
  money path; aggregates over the whole ledger must use NUMERIC.
- Payments confirm on the order's own subaddress at the exact amount after the
  confirmation threshold. Overpayment flags for review; never auto-release.
- Escrow: route every transition through `escrow.go` `Apply`, and keep a status guard
  in the SQL `UPDATE`. Ledger legs stay double-entry and idempotent.
- Privacy: only public PGP keys are stored; the server never decrypts. No client IPs,
  no email, no third-party assets, no JavaScript.
- TLS: self-signed ECDSA P-384 with the exact onion SAN. No CA certs, no SPKI pins,
  no HSTS, no warning bypass.
- Friction: mint a CAPTCHA on the GET that renders the form, or use CSRF + honeypot
  only. An un-mintable challenge silently swallows submissions.
- Templates: no JS; `<details>` for disclosure; `user-select:all` for copy blocks.

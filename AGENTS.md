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
- **TLS policy:** Tor Browser only (policy; not reliably enforceable). Generate self-signed ECDSA P-384 TLS cert with exact v3 onion DNS SAN using `scripts/gen-certs.sh`. This random TLS key is separate from Tor Ed25519 identity; Tor authenticates the onion endpoint at protocol layer. Never use public CA-issued certs or SPKI/certificate hash pinning. Never tell users to bypass a warning or import an unverified root. Verify exact supported Tor Browser release on a clean profile; if warning appears, stop and resolve compatibility.

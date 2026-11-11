# AI Agent Rules & Constraints

## 1. Absolute prohibitions
- **No JavaScript:** Do not write, suggest, or import JavaScript, TypeScript, React, Vue, Alpine, or HTMX.
- **No CDNs:** Serve CSS, fonts, and images locally.
- **No IP tracking:** Never log, store, or use the client IP address.

## 2. Go and backend standards
- Go toolchain is pinned by `go.mod` and CI. The current Go 1.25.x pin is a project/tooling decision; do not claim individual dependencies require that version unless verified from their module metadata.
- Keep routing on chi and use explicit method/middleware checks.
- Use `html/template` for HTML rendering, Argon2id for password hashing, and pgx/sqlc for database work. Do not use GORM.
- Handle errors explicitly; use context for database and external API calls.

## 3. Frontend and UI standards
- Use semantic HTML and standard POST forms for state changes.
- Include CSRF hidden inputs in every POST form using Go template syntax.
- Use CSS `:checked` or `<details>/<summary>` for interactivity.
- CAPTCHA challenges are locally generated, single-use, session-bound, and short-lived. Do not use external CAPTCHA/CDN services. Registration, ticket, and message forms use the documented honeypot field.
- Ticket/message content is escaped plain text; user URLs remain inert. Unread badges are rendered server-side.

## 4. Security and Tor operations
- Never log passwords, private PGP keys, or sensitive user data.
- Set `HttpOnly`, `Secure`, and `SameSite=Strict` on cookies. Do not emit browser-facing HSTS without a safe, tested onion TLS recovery path.
- Session IDs require at least 128 bits of CSPRNG entropy; rotate on login/privilege change and enforce idle and absolute expiry.
- The server stores PGP public keys only; private key material must never touch the server.
- App listeners must use a UNIX socket or loopback TCP except an explicitly reviewed contained namespace. Never bind a public host interface.
- TLS identity policy: do not claim or implement SPKI pinning. The current cert script generates a generic self-signed test certificate, not an onion-authenticated certificate. Do not describe it as production-authenticating or ask users to bypass certificate warnings. Production requires a separately verified onion-authenticated trust mechanism.

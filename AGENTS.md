# AI Agent Rules & Constraints

## 1. Absolute Prohibitions
- **NO JAVASCRIPT:** Do not write, suggest, or import ANY JavaScript, TypeScript, React, Vue, Alpine, or HTMX. 
- **NO CDNs:** All CSS, fonts, and images must be served locally.
- **NO IP TRACKING:** Never log, store, or use the client IP address. All traffic comes from `127.0.0.1` via Tor.

## 2. Go & Backend Standards
- Use `html/template` for all rendering (never `text/template`) to ensure automatic XSS escaping.
- Use `golang.org/x/crypto/argon2` for password hashing.
- Use `jackc/pgx` or `sqlc` for database interactions. Do not use GORM.
- Handle all errors explicitly. Do not use `_ =` to ignore errors in production code.
- Use `context.Context` for all database and external API calls to prevent goroutine leaks.

## 3. Frontend & UI Standards
- Use semantic HTML5.
- Use standard HTML forms (`method="POST"`) for all state-changing actions.
- Use URL query parameters (`?page=2&sort=price`) for filtering and pagination.
- Include a CSRF hidden input in every POST form using Go template syntax —
  `{{/* html/template, NOT Django syntax */}}`:
  ```html
  <form method="POST" action="/cart/add">
    <input type="hidden" name="csrf_token" value="{{.CsrfToken}}">
    ...
  </form>
  ```
  Never write `{% csrf_token %}`; that is Django template syntax and does not exist in `html/template`.
- Use CSS `:checked` or `<details>/<summary>` for any UI interactivity (dropdowns, accordions).
- **CAPTCHA without JavaScript:** challenges are server-rendered — a locally generated image
  (`/captcha/<token>.png`, served from `img-src 'self'`) plus a form answer; tokens are
  single-use, bound to the session, and expire in minutes. Never embed external CAPTCHA
  services (hCaptcha/reCAPTCHA/Turnstile) — they are CDNs and deanonymizing. Honeypot text
  inputs (`name="sta"`, hidden via CSS only) accompany registration, support-ticket, and
  message forms as cheap secondary friction.
- **Messaging & ticketing forms:** every message/ticket-reply `POST` carries CSRF token +
  CAPTCHA + honeypot; message bodies render as escaped plain text; user-posted URLs must be
  output as inert text (never auto-linked) to resist phishing lures. Unread-count badges are
  computed server-side per page render (no polling, no JS).

## 4. Security & Tor OpSec
- Never use `fmt.Println` or `log.Printf` to log sensitive user data, passwords, or PGP keys.
- Always validate and sanitize inputs on the server side.
- **Cookie flags:** Set `HttpOnly` and `SameSite=Strict` on every cookie.
  Do **NOT** set `Secure` on cookies for the .onion deployment: the site is served over plain
  HTTP inside the Tor circuit (Tor provides transport encryption end-to-end), and browsers
  reject the `Secure` attribute on non-HTTPS origins, which would silently break sessions.
  If HTTPS is ever added (e.g., self-signed or HSTS-preloaded setup), flip `Secure` on then.
- Session IDs: generate with ≥128 bits of entropy from `crypto/rand`, rotate the session on
  login and on privilege change, and enforce both idle and absolute expiry.
- Password hashing: `argon2id` with a per-user random salt; baseline parameters
  `time=3, memory=64MB, threads=4, keyLen=32`. Store parameters alongside the hash so they can
  be upgraded over time. Hash nonexistent users too (dummy verify) to equalize login timing.
- PGP: the server stores only **public** keys. Decryption of shipping addresses happens
  client-side (vendor's own machine); private key material must never touch the server.

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
- Include `{% csrf_token %}` (or equivalent Go CSRF token) in every POST form.
- Use CSS `:checked` or `<details>/<summary>` for any UI interactivity (dropdowns, accordions).

## 4. Security & Tor OpSec
- Never use `fmt.Println` or `log.Printf` to log sensitive user data, passwords, or PGP keys.
- Always validate and sanitize inputs on the server side.
- Ensure all cookies are set with `Secure`, `HttpOnly`, and `SameSite=Strict` flags.

# OnionShop design system

- All presentation assets are local and shared uniformly; do not add scripts, third-party fonts, or CDN links.
- Use semantic landmarks, clear heading hierarchy, explicit form labels, visible keyboard focus, responsive layouts, and reduced-motion support.
- Keep product copy and user content as plain text; URLs remain inert. `html/template` escaping is mandatory.
- Forms remain standard POST/GET forms and include CSRF/CAPTCHA/honeypot requirements from `AGENTS.md`.
- The stylesheet currently relies on inline styles in some legacy templates; retain `style-src 'unsafe-inline'` until all inline style attributes are removed and checked. Do not tighten CSP prematurely.

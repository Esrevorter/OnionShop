=========== unwrap control-flow actions ===========
   unwrapped 2 action(s) in page_account.html

parser gate
   OK

full gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1

de clutter: remove transient reports and fix scripts (keep vet/ harness)
   removed 22 transient files
   .gitignore: *_REPORT.md

deploy + verify
   login: ok
   empty crypto boxes: 0
   key button: <button type="submit">Replace Key</button>
   VERIFIED origin/main == HEAD

## Header alignment
Messages anchor, Log out submit and currency buttons now share identical padding, radius, line-height and inline-flex centering, so they sit on one baseline at equal height.

## Design system
polish.css replaced with a token-driven system: onion-violet primary, Monero-orange and ledger-green surfaces, semantic lime/amber/red/cyan, layered radial background, sticky blurred header, themed scrollbars, focus rings, responsive grid and print styles. Functional guards (honeypot, empty-crypto, button descendants, header control alignment) retained. Verified: served CSS contains tokens; /account renders no empty crypto boxes and no inputs inside buttons.

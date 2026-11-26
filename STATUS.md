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
=========== admin role + friction: final ===========

1) friction contract

2) duplicate error banners
   removed 1 page-level .Error block(s): page_admin_users.html
   removed 1 page-level .Error block(s): page_admin_vendors.html
   removed 1 page-level .Error block(s): page_admin_withdrawals.html

3) 'mod' role

4) static verification
   mismatches: 0
   page-level .Error blocks remaining: 0

5) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   OK   docker compose config --quiet

6) live proof
   live proof skipped: <urlopen error [Errno 111] Connection refused>

7) commit / push
   VERIFIED origin/main == HEAD

Applied:
 - Single error banner on page_admin_users.html
 - Single error banner on page_admin_vendors.html
 - Single error banner on page_admin_withdrawals.html

Re-login as deoperatortest so the session carries 'mod'.
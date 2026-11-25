=========== template residue repair ===========

1) scanning every template for orphaned delimiters
   page_admin_users.html: 1 orphan(s)
      line 10 [}}]  }}
   page_admin_vendors.html: 1 orphan(s)
      line 9 [}}]  }}
   page_admin_withdrawals.html: 1 orphan(s)
      line 10 [}}]  }}
   removed 3 orphan fragment(s) total

2) block balance ({{if/range/with/define/block}} vs {{end}})
   all balanced

3) html/template parse proof (same engine the app uses)
   all page templates parse cleanly

4) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   OK   docker compose config --quiet

5) rendered-page proof
   live render check skipped: <urlopen error [Errno 111] Connection refused>

6) commit / push
   VERIFIED origin/main == HEAD

Applied:
 - Removed 1 orphan delimiter(s) from page_admin_users.html
 - Removed 1 orphan delimiter(s) from page_admin_vendors.html
 - Removed 1 orphan delimiter(s) from page_admin_withdrawals.html
 - Every page template parses with html/template
 - make tplcheck added as a permanent guard
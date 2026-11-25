=========== dead-closure repair ===========
   internal/server/admin_handlers.go:114 -> removed dead closure fail
   build OK

gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   OK   docker compose config --quiet

live proof: admin sets deoperatortest -> mod
   live proof skipped: <urlopen error [Errno 111] Connection refused>

commit / push
   VERIFIED origin/main == HEAD

Applied:
 - removed dead closure fail (admin_handlers.go)

Re-login as deoperatortest so the session carries 'mod'.
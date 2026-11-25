=========== triage round 2 ===========

1) order detail: allow mod/admin (they manage orders but were 404'd)
   privileged order-detail fallback added

2) withdrawal route mounting
   withdrawal routes now mount from the pool-backed store unconditionally

3) vendor PGP key
   generated armor bytes: 631
   key saved to /tmp/vet_fixture_key.asc for the vendor vet step
   vendor login: ok
   PGP upload: ok

4) evidence capture (500s, reviews marker, tickets link)
   template link: page_admin_tickets.html -> href="/admin/tickets/{{.IDStr}}"
   template link: page_admin_tickets.html -> href="/admin/tickets/{{.IDStr}}"

5) gates
   FAIL go build ./...
# github.com/example/onionshop/internal/server
internal/server/auth_handlers.go:405:33: cannot use NewPGCatalogStore(pool) (value of type *PGCatalogStore) as WithdrawalStore value in argument to a.RegisterWithdrawalRoutes: *PGCatalogStore does not implement WithdrawalStore (missing method VendorGetByUser)

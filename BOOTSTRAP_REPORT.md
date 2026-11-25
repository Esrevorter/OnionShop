=========== vet fixture bootstrap + evidence ===========

1) evidence: withdrawal routing
   auth_handlers.go:405  a.RegisterWithdrawalRoutes(r, ws)
   withdrawal_handlers.go:37  func (a *App) RegisterWithdrawalRoutes(r chi.Router, store WithdrawalStore) {
   route Post /wallet/withdraw
   route Get /admin/withdrawals
   route Post /admin/withdrawals/{id}/review
   page_wallet.html forms:
     method="post" action="/wallet/withdraw" autocomplete="off"

2) evidence: reviews query vs live schema
   reviews columns: id, order_item_id, product_id, reviewer_id, rating, body, created_at, updated_at

3) evidence: dead links from last run
   vet/vet_buyer.md: | spider: no dead internal links | **FAIL** | [('/orders/bdff9b8f-8292-4a9b-94ed-a1d7c6925756', 404), ('/orders/8b26c0ab-a212-41cf-88bb-f0657040ee7a', 404), ('/orders/e84892b6-5285-49c1-954b-d3d95e788
   vet/vet_buyer.md: ### [MEDIUM] Dead internal links
   vet/vet_vendor.md: | spider: no dead internal links | **FAIL** | [('/vendor/shipping', 500), ('/vendor/stats', 500)] |
   vet/vet_vendor.md: ### [MEDIUM] Dead internal links
   vet/vet_admin.md: | spider: no dead internal links | **FAIL** | [('/admin/withdrawals', 404), ('/vendor/shipping', 500), ('/vendor/stats', 500), ('/admin/tickets/', 404)] |
   vet/vet_admin.md: ### [MEDIUM] Dead internal links

4) harness detail capture
   statuses now recorded in check details

5) fixture bootstrap
   admin login: ok
   vendor registration fee set to 0 (gate disabled): ok
   vendor login: ok
   created shipping option: ok
   /vendor/products error marker: no

6) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1

7) re-running triage
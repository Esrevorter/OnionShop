=========== SURGICAL DIAGNOSTIC ===========

1) Checking withdrawal route registration
   ✓ RegisterWithdrawalRoutes found in auth_handlers.go
   ✓ Routes are being registered

2) Checking vendor handler source
   ✓ vendor_handlers.go exists
   ✗ handleVendorShipping not found
   ✗ handleVendorStats not found

3) Capturing live app errors
   No obvious errors in recent logs

4) Checking database state
   Vendor devendortest: devendortest|t|631
   Vendor fee: NOT SET
   Schema: applied=22, disk_max=22

5) Fixing vendor handlers

6) Checking wallet form
   ✓ Wallet template has withdraw form

7) Gates
   OK   go build ./...
   OK   go vet ./internal/server
   FAIL go test ./internal/server/... -count=1
_account.html:46: unexpected {{else}}
--- FAIL: TestLoginHistoryWriteErrorDoesNotBlockLogin (0.60s)
    auth_handlers_test.go:520: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestLoginHoneypotSilentlyRejected (0.59s)
    auth_handlers_test.go:548: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestBuildTemplatesSucceedsOnShippedWebTemplates (0.00s)
    auth_handlers_test.go:613: buildTemplates(/home/oniondev/OnionShop/web/templates): server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestRegisterAuthRoutesNilStoreLogsAndSkips (0.00s)
    auth_handlers_test.go:670: nil-store registration returned error server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}; want log-and-skip
--- FAIL: TestFrictionContractOnRenderedForms (0.07s)
    friction_contract_test.go:22: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestPGPUploadAndClearFullChain (0.06s)
    pgp_test.go:201: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestPGPUploadStoreFailureRendersFreshChallenge (0.06s)
    pgp_test.go:274: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestPGPToolPageServesExplainer (0.00s)
    pgp_test.go:306: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestPGPRoutesRequireSession (0.00s)
    pgp_test.go:323: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
--- FAIL: TestRenderContract (0.00s)
    render_contract_test.go:12: buildTemplates: server: parse template ../../web/templates/page_account.html: template: page_account.html:46: unexpected {{else}}
FAIL
FAIL	github.com/example/onionshop/internal/server	2.908s
FAIL

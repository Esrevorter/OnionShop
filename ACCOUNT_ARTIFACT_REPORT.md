=========== account artifact fix ===========

1) making crypto wraps conditional on non-empty values
   web/templates/page_account.html
   web/templates/page_admin_withdrawals.html

2) moving inputs out of <button> elements

3) checkbox/radio styling
   web/static/polish.css

4) gates + deploy
   OK   go build ./...
   FAIL go test ./internal/server/... -count=1
unt.html: template: page_account.html:67: unexpected "{" in if; want log-and-skip
--- FAIL: TestFrictionContractOnRenderedForms (0.10s)
    friction_contract_test.go:22: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:67: unexpected "{" in if
--- FAIL: TestPGPUploadAndClearFullChain (0.05s)
    pgp_test.go:201: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:67: unexpected "{" in if
--- FAIL: TestPGPUploadStoreFailureRendersFreshChallenge (0.06s)
    pgp_test.go:274: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:67: unexpected "{" in if
--- FAIL: TestPGPToolPageServesExplainer (0.00s)
    pgp_test.go:306: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:67: unexpected "{" in if
--- FAIL: TestPGPRoutesRequireSession (0.00s)
    pgp_test.go:323: RegisterAuthRoutes: server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:67: unexpected "{" in if
--- FAIL: TestRenderContract (0.00s)
    render_contract_test.go:12: buildTemplates: server: parse template ../../web/templates/page_account.html: template: page_account.html:67: unexpected "{" in if
FAIL
FAIL	github.com/example/onionshop/internal/server	2.310s
FAIL

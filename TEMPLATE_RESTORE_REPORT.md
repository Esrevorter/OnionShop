=========== restore templates + surgical fix ===========

1) restoring web/templates to HEAD
   git checkout rc=0

2) conditional crypto wraps (correct syntax)
   wrapped conditionally: page_account.html
   wrapped conditionally: page_admin_withdrawals.html

3) inputs out of buttons

4) CSS: hide empty crypto blocks
   web/static/polish.css

5) template parser gate
   PARSE FAIL — reverting templates to HEAD:
--- FAIL: TestBuildTemplatesSucceedsOnShippedWebTemplates (0.00s)
    auth_handlers_test.go:613: buildTemplates(/home/oniondev/OnionShop/web/templates): server: parse template /home/oniondev/OnionShop/web/templates/page_account.html: template: page_account.html:67: unexpected <if> in if
FAIL
FAIL	github.com/example/onionshop/internal/server	0.010s
FAIL

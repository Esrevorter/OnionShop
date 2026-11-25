=========== friction aftercare ===========

1) restore CAPTCHA on vendor content actions (their GET mints it)
   + handleProductCreate gated with productPurpose
   + handleProductUpdate gated with productPurpose
   + handleOrderShip gated with "order"
   handleOrderExtend: already gated
   handleOrderRelease: already gated
   handleOrderCancel: already gated

2) admin forms: remove pointless CAPTCHA, keep CSRF + honeypot
   page_admin_categories.html: 1 form(s) de-CAPTCHA'd
   page_admin_dispute_detail.html: 2 form(s) de-CAPTCHA'd
   page_admin_ticket_detail.html: 3 form(s) de-CAPTCHA'd
   page_admin_users.html: 1 form(s) de-CAPTCHA'd

3) build repair
# github.com/example/onionshop/internal/server
internal/server/admin_handlers.go:114:2: declared and not used: fail


4) gates
   FAIL go build ./...
# github.com/example/onionshop/internal/server
internal/server/admin_handlers.go:114:2: declared and not used: fail

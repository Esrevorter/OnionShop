=========== template syntax repair ===========

1) fixing malformed conditional crypto wraps
   fixed web/templates/page_account.html
   fixed web/templates/page_order_detail.html
   fixed web/templates/page_wallet.html

2) validating template syntax
   FAIL web/templates/page_account.html
   fork/exec /tmp/go-build3072345917/b001/exe/main: permission denied

   FAIL web/templates/page_checkout.html
   exit status 1

   FAIL web/templates/page_login.html
   exit status 1

   FAIL web/templates/page_order_detail.html
   exit status 1

   FAIL web/templates/page_wallet.html
   exit status 1


   Templates still have parse errors. Aborting.
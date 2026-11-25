=========== audit remediation ===========

A) leaked TLS private key
   untracked nginx/ssl/onion.key and onion.crt (files kept on disk)
   .gitignore += nginx/ssl/*.key, nginx/ssl/*.crt, nginx/ssl/
   rotated TLS keypair for nicey2emajmpiz6nzmraz5zj2dkualpohaxgsyirrlze63zrjlj23iad.onion

B) Dockerfile provenance
   COPY --from=build /out/onionshop /usr/local/bin/onionshop
   COPY --from=build /out/onionshop-thumbs /usr/local/bin/onionshop-thumbs
   FALSE POSITIVE: multi-stage COPY --from=… of a freshly built binary

C) removing float from money paths
   shipping price: exact decimal parsing
   vendor fee display: integer formatting
   notification amounts: integer formatting
   + formatPiconero helper

D) friction contract
   message reply: CSRF+honeypot only (per UI.md policy)

E) checking financial/state write errors
   internal/server/dispute_handlers.go: 1 write(s) now error-checked
   internal/server/checkout_handlers.go: 3 write(s) now error-checked
   internal/server/payment_poller.go: 3 write(s) now error-checked
   internal/server/withdrawal_handlers.go: 1 write(s) now error-checked
   internal/server/order_notifications.go: 1 write(s) now error-checked

F) orders without a refund address
   ESCALATE cb652247-7a6a-4edc-98a3-6e5ee4163667|in_escrow
   ESCALATE 9b0c814d-098e-46ab-92a4-6b396c3ac383|in_escrow
   ESCALATE e84892b6-5285-49c1-954b-d3d95e788339|in_escrow

G) schema_migrations reconciliation
   1 already present in live schema - recorded
   2 already present in live schema - recorded
   3 already present in live schema - recorded
   4 already present in live schema - recorded
   FAILED 5: WARNING:  there is already a transaction in progress
ERROR:  function encode(text, unknown) does not exist
LINE 2:     ALTER COLUMN id TYPE TEXT USING encode(id
   6 already present in live schema - recorded
   7 already present in live schema - recorded
   FAILED 8: WARNING:  there is already a transaction in progress
ERROR:  column "btc_address" does not exist
   FAILED 9: WARNING:  there is already a transaction in progress
ERROR:  column "price_sat" does not exist
   applied 11
   applied 12
   applied 13
   applied 14
   applied 15
   applied 16
   applied 17
   applied 18
   applied 19
   applied 20
   FAILED 21: ERROR:  constraint "orders_btcpay_invoice_id_key" for table "orders" does not exist
   FAILED 22: NOTICE:  column "fee_sat" of relation "orders" does not exist, skipping
ERROR:  ALTER action DROP COLUMN cannot be performed on relation "account_balances"
DETA
   ledger now at version 10

H) correcting auditor false positives
   auditor: DB-only rows check, services-block parsing, multi-stage COPY, Go-side ownership, escaped inertURL

I) gates
# github.com/example/onionshop/internal/server
internal/server/dispute_handlers.go:88:4: undefined: log
internal/server/order_notifications.go:88:5: undefined: log
internal/server/shipping_handlers.go:97:2: declared and not used: priceCents
internal/server/shipping_handlers.go:98:19: undefined: priceUSD
internal/server/shipping_handlers.go:104:24: undefined: priceUSD
internal/server/withdrawal_handlers.go:161:7: undefined: log

   FAIL go build ./...
# github.com/example/onionshop/internal/server
internal/server/dispute_handlers.go:88:4: undefined: log
internal/server/order_notifications.go:88:5: undefined: log
internal/server/shipping_handlers.go:97:2: declared and not used: priceCents
internal/server/shipping_handlers.go:98:19: undefined: priceUSD
internal/server/shipping_handlers.go:104:24: undefined: priceUSD
internal/server/withdrawal_handlers.go:161:7: undefined: log

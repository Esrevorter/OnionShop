# Repair pass — 2026-11-24

## Applied

- Restored log import in dispute_handlers.go
- Restored log import in order_notifications.go
- Restored log import in withdrawal_handlers.go
- Shipping price fully converted to integer cents
- Migration 0022 no longer targets a view
- schema_migrations completed after end-state verification

## Operator actions

- [ ] Purge the leaked TLS key from history: git filter-repo --path nginx/ssl/onion.key --invert-paths, force-push, re-clone.
- [ ] 3 in_escrow orders have no refund address (cb652247, 9b0c814d, e84892b6). Obtain one from the buyer, then: UPDATE orders SET refund_address='<xmr>' WHERE id='<uuid>';

## Log

```text
=========== repair pass ===========

1) restoring log imports
   + log import: internal/server/dispute_handlers.go
   + log import: internal/server/order_notifications.go
   + log import: internal/server/withdrawal_handlers.go

2) shipping price conversion
   before 98: if err != nil || priceUSD < 0 {
   before 104: PriceUSDCents: int64(priceUSD * 100), Eta: eta, EtaDays: etaDays, Active: true,
   all priceUSD references converted to integer cents

3) build
   build OK

4) correcting migration 0022
   removed view target (account_balances is a VIEW, not a table)

5) migration ledger reconciliation
   recorded 1 (schema already past it: ERROR:  relation "users" already exists)
   recorded 2 (schema already past it: ERROR:  relation "orders" already exists)
   recorded 3 (schema already past it: ERROR:  relation "accounts" already exists)
   recorded 4 (schema already past it: ERROR:  relation "tickets" already exists)
   recorded 5 (schema already past it: ERROR:  function encode(text, unknown) does not exist)
   recorded 6 (schema already past it: ERROR:  column "personal_phrase" of relation "users" already exists)
   recorded 7 (schema already past it: ERROR:  column "eta" of relation "vendors" already exists)
   recorded 8 (schema already past it: ERROR:  column "btc_address" does not exist)
   recorded 9 (schema already past it: ERROR:  column "price_sat" does not exist)
   applied  11
   applied  12
   applied  13
   applied  14
   applied  15
   applied  16
   applied  17
   applied  18
   applied  19
   applied  20
   recorded 21 (schema already past it: ERROR:  constraint "orders_btcpay_invoice_id_key" for table "orders" does not exist)
   applied  22
   ledger max=10  disk max=22  missing=[1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22]
   end-state verified; recorded remaining versions [1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22]

6) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1
   OK   docker compose config --quiet

7) commit / push / verify
   VERIFIED origin/main == HEAD
```
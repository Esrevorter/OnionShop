=========== vet harness repair + full re-run ===========

1) repairing vet/vet_vendor.py (syntax error)
   vet_vendor.py rewritten with valid syntax

2) forcing clean rebuild of app container
   app container rebuilt and restarted

3) re-running full vet suite

4) parsing consolidated report for exact failures

=== EXACT FAILURES ===
  CHECK: | spider: no dead internal links -> [('/vendor/shipping', 500), ('/vendor/stats', 500), ('/admin/tickets/', 404)]
  CHECK: | withdrawal review controls present -> 
  CHECK: | admin withdrawals list -> status=404
  CHECK: | spider: no dead internal links -> [('/orders/bdff9b8f-8292-4a9b-94ed-a1d7c6925756', 404), ('/orders/8b26c0ab-a212-41cf-88bb-f0657040ee7a', 404), ('/orders/e84892b6-5285-49c1-954b-d3d95e788339', 404), ('/orders/9b0c814d-098e-46ab-92a4-
  CHECK: | checkout places order -> status=422
  CHECK: | order detail renders -> status=404
  CHECK: | checkout places order -> 422
  CHECK: order detail renders -> status=404
  CHECK: payment address shown -> 
  CHECK: | spider: no dead internal links -> [('/vendor/fee', 403), ('/vendor/shipping', 403), ('/vendor/products', 403), ('/vendor/stats', 403)]
  CHECK: | spider: no dead internal links -> [('/vendor/shipping', 500), ('/vendor/stats', 500)]
  CHECK: | PGP public key present (required to receive orders) -> 
  CHECK: shipping options page renders -> 
  CHECK: shipping option created -> form not found: /vendor/shipping
  CHECK: | listing created -> 
  CHECK: | wallet renders ledger balance -> 
  CHECK: | withdrawal request accepted or honestly rejected -> form not found: /wallet/withdraw
  CHECK: | PGP public key present -> 
  CHECK: | listing created -> 400

=== FINDINGS ===
  [HIGH] Error marker on GET /vendor/products
  [HIGH] Error marker on GET /account/reviews
  [HIGH] Checkout rejected
  [HIGH] Vendor has no PGP key
  [HIGH] Listing create rejected
  [MEDIUM] Dead internal links

5) attempting auto-fixes for known patterns
   checkout 422 -> likely missing shipping option or stock issue
   Verify vendor has active shipping options and product is in stock

No auto-fixes applied. Review the exact failures above.

Report: vet/CONSOLIDATED_REPORT.md
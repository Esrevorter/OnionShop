-- Downgrade: revert to satoshi-based schema
BEGIN;
ALTER TABLE products RENAME COLUMN price_usd_cents TO price_sat;
ALTER TABLE order_items RENAME COLUMN unit_price_usd_cents TO unit_price_sat;
ALTER TABLE orders RENAME COLUMN total_usd_cents TO total_sat;
ALTER TABLE orders DROP COLUMN xmr_amount_piconero;
ALTER TABLE ledger_entries RENAME COLUMN amount_piconero TO amount_sat;
ALTER TABLE withdrawals RENAME COLUMN amount_piconero TO amount_sat;
COMMIT;

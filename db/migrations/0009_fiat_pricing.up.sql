-- 0009_fiat_pricing.sql — Shift from satoshi-based to fiat-pegged/XMR-settled
BEGIN;

-- Products & Order Items: price is now stored in USD cents (base unit of account)
ALTER TABLE products RENAME COLUMN price_sat TO price_usd_cents;
ALTER TABLE order_items RENAME COLUMN unit_price_sat TO unit_price_usd_cents;

-- Orders: track fiat total and the locked XMR settlement amount
ALTER TABLE orders RENAME COLUMN total_sat TO total_usd_cents;
ALTER TABLE orders ADD COLUMN xmr_amount_piconero BIGINT NOT NULL DEFAULT 0 
    CHECK (xmr_amount_piconero >= 0);
COMMENT ON COLUMN orders.xmr_amount_piconero IS 'Locked XMR amount in piconeros (1 XMR = 10^12 piconero) for settlement';

-- Ledger & Withdrawals: track actual XMR movement (piconeros) instead of satoshis
ALTER TABLE ledger_entries RENAME COLUMN amount_sat TO amount_piconero;
ALTER TABLE withdrawals RENAME COLUMN amount_sat TO amount_piconero;

COMMIT;

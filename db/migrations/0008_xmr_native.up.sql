-- 0008_xmr_native.sql — Transition from BTC/BTCPay to Monero (XMR)
-- This renames payment columns to be XMR-native, aligning with the privacy-first
-- architecture of the marketplace.
BEGIN;

-- Orders: replace BTCPay-specific columns with XMR equivalents
ALTER TABLE orders RENAME COLUMN btc_address TO xmr_address;
ALTER TABLE orders RENAME COLUMN btcpay_invoice_id TO xmr_payment_id;
COMMENT ON COLUMN orders.xmr_address IS 'Monero subaddress for this specific vendor order';
COMMENT ON COLUMN orders.xmr_payment_id IS 'Monero payment ID or subaddress index for tracking';

-- Payment Events: make provider-agnostic / XMR-native
ALTER TABLE payment_events RENAME COLUMN invoice_id TO payment_id;
ALTER TABLE payment_events RENAME COLUMN btcpay_status TO payment_status;
COMMENT ON COLUMN payment_events.payment_id IS 'Monero payment ID or tx hash';
COMMENT ON COLUMN payment_events.payment_status IS 'Status from monero-wallet-rpc (e.g., confirmed)';

-- Withdrawals: vendor payouts in XMR
ALTER TABLE withdrawals RENAME COLUMN btc_address TO xmr_address;
COMMENT ON COLUMN withdrawals.xmr_address IS 'Vendor Monero payout address';

COMMIT;

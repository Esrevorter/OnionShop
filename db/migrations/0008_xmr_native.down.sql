-- Downgrade: revert XMR columns back to BTC/BTCPay names
BEGIN;
ALTER TABLE orders RENAME COLUMN xmr_address TO btc_address;
ALTER TABLE orders RENAME COLUMN xmr_payment_id TO btcpay_invoice_id;
ALTER TABLE payment_events RENAME COLUMN payment_id TO invoice_id;
ALTER TABLE payment_events RENAME COLUMN payment_status TO btcpay_status;
ALTER TABLE withdrawals RENAME COLUMN xmr_address TO btc_address;
COMMIT;

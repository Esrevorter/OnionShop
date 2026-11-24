-- Testnet/stagenet addresses exceed mainnet length; payment id is now an order UUID.
ALTER TABLE orders ALTER COLUMN xmr_address TYPE TEXT;
ALTER TABLE orders ALTER COLUMN xmr_payment_id TYPE TEXT;

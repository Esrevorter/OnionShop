ALTER TABLE orders ADD COLUMN IF NOT EXISTS fee_sat BIGINT;
ALTER TABLE account_balances ADD COLUMN IF NOT EXISTS balance_sat BIGINT;
-- payment_events was an empty BTCPay-era table; see migration 0002.

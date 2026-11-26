-- BTCPay-era residue with zero code references (ADR-002).
ALTER TABLE orders DROP COLUMN IF EXISTS fee_sat;
DROP TABLE IF EXISTS payment_events;


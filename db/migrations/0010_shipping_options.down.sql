BEGIN;
ALTER TABLE orders DROP COLUMN shipping_usd_cents;
ALTER TABLE orders DROP COLUMN shipping_option_id;
DROP TABLE shipping_options;
COMMIT;

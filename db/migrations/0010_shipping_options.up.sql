-- 0010_shipping_options.sql — vendor-defined shipping profiles
BEGIN;

CREATE TABLE shipping_options (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id       UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
    title           TEXT NOT NULL CHECK (char_length(title) BETWEEN 2 AND 80),
    description     TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    price_usd_cents BIGINT NOT NULL CHECK (price_usd_cents >= 0),
    eta             TEXT NOT NULL DEFAULT 'medium' CHECK (eta IN ('fast','medium','slow')),
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX shipping_options_vendor_idx ON shipping_options (vendor_id);

-- Each vendor-scoped order records which option the buyer chose and its cost,
-- so the locked XMR amount and ledger always include shipping.
ALTER TABLE orders ADD COLUMN shipping_option_id UUID REFERENCES shipping_options(id);
ALTER TABLE orders ADD COLUMN shipping_usd_cents BIGINT NOT NULL DEFAULT 0
    CHECK (shipping_usd_cents >= 0);

COMMIT;

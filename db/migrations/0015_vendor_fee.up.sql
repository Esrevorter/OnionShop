CREATE TABLE IF NOT EXISTS site_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO site_settings(key, value) VALUES ('vendor_fee_usd_cents','0')
    ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS vendor_fee_payments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id       UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
    amount_piconero BIGINT NOT NULL CHECK (amount_piconero > 0),
    xmr_address     TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'awaiting_payment'
                    CHECK (status IN ('awaiting_payment','paid','expired')),
    tx_hash         TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at         TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS vendor_fee_active_idx
    ON vendor_fee_payments(vendor_id) WHERE status = 'awaiting_payment';
CREATE UNIQUE INDEX IF NOT EXISTS vendor_fee_paid_idx
    ON vendor_fee_payments(vendor_id) WHERE status = 'paid';

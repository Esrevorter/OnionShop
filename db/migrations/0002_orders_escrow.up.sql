-- 0002_orders_escrow.sql — multi-vendor orders and the escrow state machine
-- (ARCHITECTURE.md §4). Status transitions are enforced here in the DB as a
-- second line of defense behind the Go service.
BEGIN;

CREATE TABLE orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_ref   UUID NOT NULL,                 -- shared across N vendor orders of one cart
    customer_id    UUID NOT NULL REFERENCES users(id),
    vendor_id      UUID NOT NULL REFERENCES vendors(id),
    status         TEXT NOT NULL DEFAULT 'awaiting_payment' CHECK (status IN
                   ('awaiting_payment','in_escrow','shipped','disputed',
                    'released','refunded','cancelled')),
    total_sat      BIGINT NOT NULL CHECK (total_sat > 0),
    fee_sat        BIGINT NOT NULL DEFAULT 0 CHECK (fee_sat >= 0),
    btc_address    TEXT,                           -- invoice payout info from BTCPay
    btcpay_invoice_id TEXT UNIQUE,                 -- our poll target (ARCHITECTURE.md §2)
    shipping_ciphertext TEXT,                      -- PGP-encrypted with VENDOR public key
    shipped_at     TIMESTAMPTZ,
    auto_release_at TIMESTAMPTZ,                   -- shipped_at + 14d, UTC from Postgres
    cancelled_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_customer_idx ON orders (customer_id);
CREATE INDEX orders_vendor_idx   ON orders (vendor_id);
CREATE INDEX orders_status_idx   ON orders (status);
-- Cron scan for auto-release: shipped orders past their deadline.
CREATE INDEX orders_auto_release_idx ON orders (auto_release_at)
    WHERE status = 'shipped';

CREATE TABLE order_items (
    id           BIGSERIAL PRIMARY KEY,
    order_id     UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id   UUID NOT NULL REFERENCES products(id),
    product_title TEXT NOT NULL,   -- denormalized snapshot at purchase time
    qty          INT NOT NULL CHECK (qty > 0),
    unit_price_sat BIGINT NOT NULL CHECK (unit_price_sat > 0)
);

-- Idempotency ledger for payment events derived from BTCPay polls. A duplicate
-- or replayed (invoice_id, status) can never be applied twice.
CREATE TABLE payment_events (
    id              BIGSERIAL PRIMARY KEY,
    invoice_id      TEXT NOT NULL,
    btcpay_status   TEXT NOT NULL,
    order_id        UUID NOT NULL REFERENCES orders(id),
    paid_amount_sat BIGINT,
    raw_payload     JSONB NOT NULL,
    applied         BOOLEAN NOT NULL DEFAULT TRUE,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (invoice_id, btcpay_status)
);

CREATE TABLE disputes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES orders(id),
    opened_by   UUID NOT NULL REFERENCES users(id),
    reason      TEXT NOT NULL CHECK (char_length(reason) BETWEEN 5 AND 4000),
    resolution  TEXT CHECK (resolution IN ('released','refunded')),
    resolved_by UUID REFERENCES users(id),
    resolved_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id, opened_by)
);

CREATE TABLE dispute_messages (
    id         BIGSERIAL PRIMARY KEY,
    dispute_id UUID NOT NULL REFERENCES disputes(id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users(id),
    body       TEXT NOT NULL CHECK (char_length(body) <= 8000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;

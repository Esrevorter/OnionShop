ALTER TABLE orders ADD COLUMN IF NOT EXISTS extended BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS auto_release_at TIMESTAMPTZ;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS shipped_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS order_messages (
    id BIGSERIAL PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    sender_type TEXT NOT NULL CHECK (sender_type IN ('system', 'buyer', 'vendor', 'admin')),
    body TEXT NOT NULL CHECK (char_length(body) <= 5000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS order_messages_order_idx ON order_messages(order_id, created_at);

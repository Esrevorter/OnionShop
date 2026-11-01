-- db/schema.sql — canonical schema for sqlc (sqlc.yaml: schema: "db/schema.sql").
--
-- This is the Postgres state after applying every migration in db/migrations/
-- (0001–0005), flattened so sqlc's built-in parser can resolve all queries.
-- The live schema is still managed by golang-migrate (make migrate); keep this
-- file in sync whenever a new migration lands. Notably, migration 0005 rewrites
-- sessions.id to TEXT and sessions.data to BYTEA and adds sessions.updated_at;
-- that end state is reflected inline below rather than via ALTERs, because the
-- sqlc parser does not support ALTER COLUMN TYPE.

CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- gen_random_uuid()

-- ---------------------------------------------------------------------------
-- 0001_core_schema — users, RBAC, vendors, products, categories
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_]{3,32}$'),
    password_hash   TEXT NOT NULL,             -- argon2id encoded w/ params (AGENTS.md §4)
    role            TEXT NOT NULL DEFAULT 'customer'
                    CHECK (role IN ('customer','vendor','mod','admin')),
    pgp_public_key  TEXT,                       -- armored PUBLIC key only; private keys never stored
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('pending','active','suspended','banned')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Sessions fallback table (primary store is Redis; see ARCHITECTURE.md §2).
-- Column types reflect migration 0005 applied on top of 0001:
--   id         BYTEA -> TEXT   (scs tokens are base64url strings)
--   data       JSONB -> BYTEA  (scs gob payload; default dropped)
--   updated_at added           (last-write marker for Redis warm-back order)
CREATE TABLE sessions (
    id            TEXT PRIMARY KEY,             -- scs token (base64url, >=128-bit entropy)
    user_id       UUID REFERENCES users(id) ON DELETE CASCADE,
    data          BYTEA NOT NULL,               -- scs gob-encoded session payload
    expires_at    TIMESTAMPTZ NOT NULL,         -- idle expiry, refreshed on each commit
    absolute_stop TIMESTAMPTZ NOT NULL,         -- hard lifetime cap, never extended
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE vendors (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    shop_name  TEXT NOT NULL UNIQUE CHECK (char_length(shop_name) BETWEEN 3 AND 60),
    approved   BOOLEAN NOT NULL DEFAULT FALSE,   -- set by admin; audit-logged
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE categories (
    id       SERIAL PRIMARY KEY,
    slug     TEXT NOT NULL UNIQUE,
    name     TEXT NOT NULL,
    parent_id INT REFERENCES categories(id)
);

CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id   UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
    category_id INT  REFERENCES categories(id),
    title       TEXT NOT NULL CHECK (char_length(title) BETWEEN 3 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    price_sat   BIGINT NOT NULL CHECK (price_sat > 0),      -- integer satoshis, never float
    stock       INT NOT NULL DEFAULT 0 CHECK (stock >= 0),
    image_path  TEXT,                                       -- served from web/static only
    status      TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('draft','active','removed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX products_vendor_idx  ON products (vendor_id);
CREATE INDEX products_category_idx ON products (category_id);
CREATE INDEX products_search_idx  ON products USING gin (to_tsvector('simple', title || ' ' || description));

-- ---------------------------------------------------------------------------
-- 0002_orders_escrow — orders, order items, payment events, disputes
-- ---------------------------------------------------------------------------

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

-- ---------------------------------------------------------------------------
-- 0003_ledger_audit — accounts, append-only ledger, withdrawals, audit log
-- ---------------------------------------------------------------------------

CREATE TABLE accounts (
    id      SERIAL PRIMARY KEY,
    name    TEXT NOT NULL UNIQUE      -- 'escrow', 'platform_fee', 'refund', 'vendor_<uuid>'
        CHECK (name ~ '^(escrow|platform_fee|refund|vendor_[0-9a-f-]{36})$'),
    kind    TEXT NOT NULL CHECK (kind IN ('asset','liability','revenue')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO accounts (name, kind) VALUES
    ('escrow', 'asset'), ('platform_fee', 'revenue'), ('refund', 'liability');

-- Immutable entries: balance = SUM(debits) - SUM(credits) per account.
CREATE TABLE ledger_entries (
    id             BIGSERIAL PRIMARY KEY,
    tx_ref         UUID NOT NULL,               -- one logical movement shares a tx_ref
    idempotency_key TEXT NOT NULL UNIQUE,       -- e.g. 'pay:<invoice_id>', 'release:<order_id>'
    account_id     INT NOT NULL REFERENCES accounts(id),
    amount_sat     BIGINT NOT NULL CHECK (amount_sat > 0),
    direction      TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    order_id       UUID REFERENCES orders(id),
    memo           TEXT NOT NULL DEFAULT '' CHECK (char_length(memo) <= 500),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ledger_account_idx ON ledger_entries (account_id, id);
CREATE INDEX ledger_tx_idx      ON ledger_entries (tx_ref);

CREATE VIEW account_balances AS
    SELECT account_id,
           COALESCE(SUM(amount_sat) FILTER (WHERE direction = 'debit'),  0)
         - COALESCE(SUM(amount_sat) FILTER (WHERE direction = 'credit'), 0) AS balance_sat
    FROM ledger_entries
    GROUP BY account_id;

CREATE TABLE withdrawals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id    UUID NOT NULL REFERENCES vendors(id),
    amount_sat   BIGINT NOT NULL CHECK (amount_sat > 0),
    btc_address  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'requested'
                 CHECK (status IN ('requested','approved','paid','rejected')),
    reviewed_by  UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Audit trail: every privileged/system action. Rows are immutable like the ledger.
CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    actor_id   UUID REFERENCES users(id),        -- NULL => automated cron (actor='system')
    actor_kind TEXT NOT NULL DEFAULT 'user' CHECK (actor_kind IN ('user','system')),
    action     TEXT NOT NULL,                    -- 'dispute.resolve','vendor.approve',...
    target     TEXT NOT NULL,                    -- '<table>:<id>'
    details    JSONB NOT NULL DEFAULT '{}',      -- NO secrets/PII here (log sanitization rules)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_target_idx ON audit_log (target, created_at);

-- ---------------------------------------------------------------------------
-- 0004_support_messaging — tickets and private messaging
-- ---------------------------------------------------------------------------

CREATE TABLE tickets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    order_ref   UUID REFERENCES orders(id),            -- optional link to an order
    category    TEXT NOT NULL CHECK (category IN
                ('general','order_problem','vendor_report','payment_issue',
                 'bug_bounty','account_recovery')),
    subject     TEXT NOT NULL CHECK (char_length(subject) BETWEEN 3 AND 200),
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN
                ('open','answered','user_replied','resolved','closed')),
    priority    TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN
                ('low','normal','high','urgent')),
    assigned_to UUID REFERENCES users(id),             -- staff member who claimed it
    dispute_id  UUID REFERENCES disputes(id),          -- set when escalated (§7)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tickets_status_idx  ON tickets(status, priority, updated_at);
CREATE INDEX tickets_user_idx    ON tickets(user_id, created_at DESC);
CREATE INDEX tickets_assigned_idx ON tickets(assigned_to) WHERE assigned_to IS NOT NULL;

CREATE TABLE ticket_messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id  UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users(id),
    body       TEXT NOT NULL CHECK (char_length(body) <= 10000),
    is_staff   BOOLEAN NOT NULL DEFAULT false,          -- denormalized from author role
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ticket_messages_ticket_idx ON ticket_messages(ticket_id, created_at);

CREATE TABLE threads (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    participant_a UUID NOT NULL REFERENCES users(id),
    participant_b UUID NOT NULL REFERENCES users(id),
    order_ref     UUID REFERENCES orders(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (participant_a < participant_b)               -- one thread per unordered pair
);

CREATE UNIQUE INDEX threads_pair_idx ON threads(participant_a, participant_b);

CREATE TABLE messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id  UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    sender_id  UUID NOT NULL REFERENCES users(id),
    body       TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 5000),
    -- URLs inside body are rendered as inert text by the template layer; never auto-linked.
    reported   BOOLEAN NOT NULL DEFAULT false,          -- "report message" -> abuse ticket
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX messages_thread_idx ON messages(thread_id, created_at);
CREATE INDEX messages_unread_idx ON messages(thread_id, read_at) WHERE read_at IS NULL;

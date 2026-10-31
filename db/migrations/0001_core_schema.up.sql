-- 0001_core_schema.sql — users, RBAC, vendors, products, categories.
BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- gen_random_uuid()

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
CREATE TABLE sessions (
    id            BYTEA PRIMARY KEY,            -- 128+ bit random token (crypto/rand)
    user_id       UUID REFERENCES users(id) ON DELETE CASCADE,
    data          JSONB NOT NULL DEFAULT '{}',
    expires_at    TIMESTAMPTZ NOT NULL,
    absolute_stop TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
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

COMMIT;

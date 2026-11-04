-- 0007_catalog_phase4.sql — Phase 4 catalog & vendors (TASKS.md).
--
-- vendor_eta: per-vendor shipping-speed badge shown on profile pages and in
--   the top-vendors leaderboard ("message vendor" and storefront links ride
--   the existing vendors/users rows; no extra table needed).
-- reviews: one review per ORDER LINE (UNIQUE order_item_id), editable inside
--   a bounded window after purchase (window enforced in the handler layer —
--   ReviewEditWindow — because "now()" belongs to the server, not the form).
--   Ratings live on completed orders only (status gate in the handlers);
--   plain HTML forms, no JS (AGENTS.md §1/§3).
-- top_vendors_snapshot: cron-maintained leaderboard materialized from
--   released orders minus disputes (ARCHITECTURE.md §9). Cheap to render on
--   the homepage; refreshed by internal/cron, never computed per request.
BEGIN;

ALTER TABLE vendors ADD COLUMN eta TEXT NOT NULL DEFAULT 'medium'
    CHECK (eta IN ('fast', 'medium', 'slow'));

CREATE TABLE reviews (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_item_id BIGINT NOT NULL REFERENCES order_items(id) ON DELETE CASCADE,
    product_id    UUID   NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    reviewer_id   UUID   NOT NULL REFERENCES users(id),
    rating        INT    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body          TEXT   NOT NULL DEFAULT '' CHECK (char_length(body) <= 2000),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX reviews_item_uniq ON reviews (order_item_id);
CREATE INDEX reviews_product_idx ON reviews (product_id, created_at DESC);

CREATE TABLE top_vendors_snapshot (
    rank         INT PRIMARY KEY,
    vendor_id    UUID NOT NULL UNIQUE REFERENCES vendors(id) ON DELETE CASCADE,
    sales_count  INT  NOT NULL CHECK (sales_count >= 0),
    avg_rating   NUMERIC(3,2) NOT NULL DEFAULT 0 CHECK (avg_rating BETWEEN 0 AND 5),
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;

-- db/query/catalog.sql — Phase 4 catalog & vendor reads/writes (TASKS.md).
-- Handlers mirror these statements verbatim in internal/server (see the
-- qCatalog* constants); keep both in sync when columns move, exactly like
-- users.sql mirrors the auth handlers' qAuth*/qVendor* constants.

-- name: UpsertVendorRequest :exec
INSERT INTO vendors (user_id, shop_name, approved, eta)
VALUES ($1, $2, FALSE, 'medium')
ON CONFLICT (user_id) DO UPDATE SET shop_name = EXCLUDED.shop_name;

-- name: GetVendorByUser :one
SELECT * FROM vendors WHERE user_id = $1;

-- name: GetVendorByID :one
SELECT * FROM vendors WHERE id = $1;

-- name: GetVendorByUsername :one
SELECT v.* FROM vendors v JOIN users u ON u.id = v.user_id
WHERE u.username = $1 AND v.approved;

-- name: ListPendingVendors :many
SELECT v.* FROM vendors v JOIN users u ON u.id = v.user_id
WHERE NOT v.approved AND u.role = 'customer'
ORDER BY v.created_at ASC LIMIT 200;

-- name: SetVendorApproval :exec
UPDATE vendors SET approved = $2 WHERE id = $1;

-- name: SetVendorETA :exec
UPDATE vendors SET eta = $2 WHERE id = $1;

-- name: ListCategories :many
SELECT * FROM categories ORDER BY slug;

-- (CreateProduct lives in products.sql — query names are package-wide in sqlc,
-- duplicates here caused "duplicate query name: CreateProduct" in CI.)

-- name: UpdateProductOwned :one
UPDATE products SET
    category_id = $3,
    title       = $4,
    description = $5,
    price_sat   = $6,
    stock       = $7,
    status      = $8,
    updated_at  = now()
WHERE id = $1 AND vendor_id = $2
RETURNING *;

-- name: RemoveProductOwned :one
-- Soft delete: status='removed' hides the listing everywhere while order_items
-- FKs stay intact (hard deletes would break review/order history).
UPDATE products SET status = 'removed', updated_at = now()
WHERE id = $1 AND vendor_id = $2 AND status <> 'removed'
RETURNING id;

-- name: GetPublicProduct :one
SELECT p.*, v.shop_name, v.eta, u.username AS vendor_username
FROM products p
JOIN vendors v ON v.id = p.vendor_id
JOIN users u   ON u.id = v.user_id
WHERE p.id = $1 AND p.status = 'active' AND v.approved;

-- name: ListStorefrontProducts :many
SELECT p.* FROM products p
JOIN vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1)
ORDER BY p.created_at DESC
LIMIT $2 OFFSET $3;

-- name: SearchStorefrontProducts :many
SELECT p.* FROM products p
JOIN vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1)
  AND to_tsvector('simple', p.title || ' ' || p.description)
      @@ plainto_tsquery('simple', $4)
ORDER BY ts_rank(to_tsvector('simple', p.title || ' ' || p.description),
                 plainto_tsquery('simple', $4)) DESC
LIMIT $2 OFFSET $3;

-- name: CountStorefrontProducts :one
SELECT count(*) FROM products p
JOIN vendors v ON v.id = p.vendor_id
WHERE p.status = 'active' AND v.approved
  AND ($1::int IS NULL OR p.category_id = $1);

-- name: VendorStats :one
-- Sales count + average rating over RELEASED orders of one vendor.
SELECT count(o.id)::int                                AS sales_count,
       COALESCE(round(AVG(r.rating), 2), 0)::float8    AS avg_rating
FROM vendors v
LEFT JOIN orders o ON o.vendor_id = v.id AND o.status = 'released'
LEFT JOIN order_items oi ON oi.order_id = o.id
LEFT JOIN reviews r ON r.order_item_id = oi.id
WHERE v.id = $1;

-- name: RefreshTopVendorsSelect :many
-- Source rows for the leaderboard snapshot (the cron rewrites
-- top_vendors_snapshot from this result; ARCHITECTURE.md §9).
SELECT v.id AS vendor_id,
       count(DISTINCT o.id)::int                           AS sales_count,
       COALESCE(round(AVG(r.rating), 2), 0)::float8        AS avg_rating
FROM vendors v
JOIN orders o ON o.vendor_id = v.id AND o.status = 'released'
LEFT JOIN order_items oi ON oi.order_id = o.id
LEFT JOIN reviews r ON r.order_item_id = oi.id
WHERE v.approved
GROUP BY v.id
HAVING count(DISTINCT o.id) > 0
ORDER BY count(DISTINCT o.id) DESC,
         COALESCE(round(AVG(r.rating), 2), 0) DESC,
         v.id
LIMIT 50;

-- name: DeleteTopVendorsSnapshot :exec
DELETE FROM top_vendors_snapshot;

-- name: InsertTopVendorRow :exec
INSERT INTO top_vendors_snapshot (rank, vendor_id, sales_count, avg_rating, refreshed_at)
VALUES ($1, $2, $3, $4, $5);

-- name: ListTopVendorsSnapshot :many
SELECT t.rank, t.sales_count, t.avg_rating, v.shop_name, v.eta, u.username AS vendor_username
FROM top_vendors_snapshot t
JOIN vendors v ON v.id = t.vendor_id
JOIN users u   ON u.id = v.user_id
ORDER BY t.rank ASC;

-- name: ReviewableLinesForCustomer :many
-- Completed-order lines the customer may rate (or re-rate inside the edit
-- window). One review per order line, enforced by reviews_item_uniq.
SELECT oi.id AS item_id, oi.product_id, oi.product_title, oi.qty,
       o.status AS order_status, o.updated_at AS order_updated_at,
       r.id IS NOT NULL                     AS has_review,
       COALESCE(r.rating, 0)::int           AS rating,
       COALESCE(r.body, '')                 AS review_body,
       r.created_at                         AS review_created_at
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p     ON p.id = oi.product_id
JOIN vendors v      ON v.id = o.vendor_id
LEFT JOIN reviews r ON r.order_item_id = oi.id AND r.reviewer_id = $1
WHERE o.customer_id = $1 AND v.id = $2
  AND o.status IN ('shipped', 'released')
ORDER BY o.created_at DESC;

-- name: GetReviewLineContext :one
-- Everything the submit handler needs to authorize ONE review write:
-- the line's order status/age, the product's owning vendor+user, and the
-- reviewer's identity. Status gate lives here so a stale form POST cannot
-- rate a cancelled/refunded order.
SELECT oi.id            AS item_id,
       oi.product_id    AS product_id,
       o.customer_id    AS reviewer_id,
       o.status         AS order_status,
       o.updated_at     AS order_updated_at,
       v.id             AS vendor_id,
       v.user_id        AS vendor_user_id
FROM order_items oi
JOIN orders o     ON o.id = oi.order_id
JOIN products p   ON p.id = oi.product_id
JOIN vendors v    ON v.id = p.vendor_id
WHERE oi.id = $1;

-- name: UpsertReview :exec
INSERT INTO reviews (order_item_id, product_id, reviewer_id, rating, body)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (order_item_id) DO UPDATE
SET rating = EXCLUDED.rating, body = EXCLUDED.body, updated_at = now();

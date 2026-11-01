-- db/query/products.sql — catalogue reads/writes (ARCHITECTURE.md §5).
-- Prices are integer satoshis end-to-end; stock checks happen inside the
-- UPDATE so a checkout can never oversell (single-statement atomicity).

-- name: CreateProduct :one
INSERT INTO products (vendor_id, category_id, title, description, price_sat, stock, image_path, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetProduct :one
SELECT * FROM products WHERE id = $1;

-- name: ListActiveProducts :many
SELECT p.* FROM products p
WHERE p.status = 'active' AND p.category_id = ANY(sqlc.arg(category_ids)::int[])
ORDER BY p.created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListVendorProducts :many
SELECT * FROM products
WHERE vendor_id = $1
ORDER BY created_at DESC;

-- name: SearchProducts :many
SELECT p.* FROM products p
WHERE p.status = 'active'
  AND to_tsvector('simple', p.title || ' ' || p.description) @@ plainto_tsquery('simple', sqlc.arg(query))
ORDER BY ts_rank(to_tsvector('simple', p.title || ' ' || p.description),
                 plainto_tsquery('simple', sqlc.arg(query))) DESC
LIMIT sqlc.arg(lim);

-- name: UpdateProduct :one
UPDATE products SET
    category_id = $2,
    title       = $3,
    description = $4,
    price_sat   = $5,
    stock       = $6,
    image_path  = $7,
    status      = $8,
    updated_at  = now()
WHERE id = $1
RETURNING *;

-- name: DecrementStock :one
-- Atomic buy-time stock check: fails (no rows) if stock is insufficient, which
-- surfaces as pgx.ErrNoRows to the caller.
UPDATE products SET stock = stock - sqlc.arg(qty), updated_at = now()
WHERE id = sqlc.arg(id) AND stock >= sqlc.arg(qty)
RETURNING *;

-- name: IncrementStock :exec
-- Restock on cancellation/refund paths.
UPDATE products SET stock = stock + sqlc.arg(qty), updated_at = now() WHERE id = sqlc.arg(id);

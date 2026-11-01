-- db/query/orders.sql — checkout, order lifecycle and the escrow state machine
-- (ARCHITECTURE.md §4). Status transitions are additionally guarded in Go;
-- the DB CHECK constraint (migration 0002) is the second line of defense.

-- name: CreateOrder :one
INSERT INTO orders (checkout_ref, customer_id, vendor_id, total_sat, fee_sat, btc_address, btcpay_invoice_id, shipping_ciphertext)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: GetOrderByInvoice :one
SELECT * FROM orders WHERE btcpay_invoice_id = $1;

-- name: ListCustomerOrders :many
SELECT * FROM orders
WHERE customer_id = $1
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListVendorOrders :many
SELECT * FROM orders
WHERE vendor_id = $1 AND status = ANY(sqlc.arg(statuses)::text[])
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: UpdateOrderStatus :exec
UPDATE orders SET status = $2, updated_at = now() WHERE id = $1;

-- name: MarkOrderShipped :exec
-- auto_release_at = shipped_at + 14d, computed by Postgres so app clock skew
-- can never shorten a buyer's dispute window.
UPDATE orders SET
    status          = 'shipped',
    shipped_at      = now(),
    auto_release_at = now() + interval '14 days',
    updated_at      = now()
WHERE id = $1;

-- name: AutoReleaseDueOrders :many
-- Cron scan backed by orders_auto_release_idx (partial index on status='shipped').
SELECT * FROM orders
WHERE status = 'shipped' AND auto_release_at <= now()
ORDER BY auto_release_at
LIMIT sqlc.arg(lim);

-- name: CreateOrderItem :exec
INSERT INTO order_items (order_id, product_id, product_title, qty, unit_price_sat)
VALUES ($1, $2, $3, $4, $5);

-- name: ListOrderItems :many
SELECT * FROM order_items WHERE order_id = $1 ORDER BY id;

-- name: InsertPaymentEvent :exec
-- UNIQUE (invoice_id, btcpay_status) makes BTCPay poll replays idempotent;
-- callers must treat a unique-violation as "already applied", not an error.
INSERT INTO payment_events (invoice_id, btcpay_status, order_id, paid_amount_sat, raw_payload, applied)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: OpenDispute :one
INSERT INTO disputes (order_id, opened_by, reason)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetDisputeByOrder :one
SELECT * FROM disputes WHERE order_id = $1;

-- name: ResolveDispute :exec
UPDATE disputes SET resolution = $2, resolved_by = $3, resolved_at = now()
WHERE id = $1 AND resolved_at IS NULL;

-- name: AddDisputeMessage :exec
INSERT INTO dispute_messages (dispute_id, author_id, body)
VALUES ($1, $2, $3);

-- name: ListDisputeMessages :many
SELECT * FROM dispute_messages WHERE dispute_id = $1 ORDER BY created_at;

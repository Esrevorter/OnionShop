package server

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SplitCartIntoVendorOrders partitions a cart by vendor and creates N vendor-scoped orders
// sharing a single checkout reference, returning the created order IDs and checkout reference.
func SplitCartIntoVendorOrders(ctx context.Context, pool *pgxpool.Pool, customerID pgtype.UUID, cart Cart) (pgtype.UUID, []pgtype.UUID, error) {
	if pool == nil {
		return pgtype.UUID{}, nil, fmt.Errorf("server: db pool required for order splitting")
	}
	if len(cart) == 0 {
		return pgtype.UUID{}, nil, fmt.Errorf("server: empty cart")
	}

	// Group items by VendorID
	vendorGroups := make(map[string][]CartItem)
	for _, item := range cart {
		vendorGroups[item.VendorID] = append(vendorGroups[item.VendorID], item)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("server: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var checkoutRef pgtype.UUID
	if err := tx.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&checkoutRef); err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("server: generate checkout ref: %w", err)
	}

	var orderIDs []pgtype.UUID

	for vendorIDStr, items := range vendorGroups {
		var vendorUUID pgtype.UUID
		if err := vendorUUID.Scan(vendorIDStr); err != nil {
			return pgtype.UUID{}, nil, fmt.Errorf("server: invalid vendor id %s: %w", vendorIDStr, err)
		}

		var totalSat int64
		for _, item := range items {
			totalSat += item.PriceSat * int64(item.Qty)
		}

		var orderID pgtype.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO orders (checkout_ref, customer_id, vendor_id, status, total_sat)
			VALUES ($1, $2, $3, 'awaiting_payment', $4)
			RETURNING id
		`, checkoutRef, customerID, vendorUUID, totalSat).Scan(&orderID)
		if err != nil {
			return pgtype.UUID{}, nil, fmt.Errorf("server: insert order: %w", err)
		}

		for _, item := range items {
			var productUUID pgtype.UUID
			if err := productUUID.Scan(item.ProductID); err != nil {
				return pgtype.UUID{}, nil, fmt.Errorf("server: invalid product id %s: %w", item.ProductID, err)
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO order_items (order_id, product_id, product_title, qty, unit_price_sat)
				VALUES ($1, $2, $3, $4, $5)
			`, orderID, productUUID, item.Title, item.Qty, item.PriceSat)
			if err != nil {
				return pgtype.UUID{}, nil, fmt.Errorf("server: insert order item: %w", err)
			}
		}

		orderIDs = append(orderIDs, orderID)
	}

	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("server: commit tx: %w", err)
	}

	return checkoutRef, orderIDs, nil
}

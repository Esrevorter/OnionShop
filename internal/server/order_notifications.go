package server

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyOrder posts lifecycle messages into the buyer<->vendor thread using the
// existing unread mechanism (unread = sender != me AND read_at IS NULL).
//   created: vendor->buyer  (buyer notified; vendor sees own sent line)
//   paid:    vendor->buyer  AND buyer->vendor (vendor notified only at escrow)
func notifyOrder(ctx context.Context, pool *pgxpool.Pool, orderID pgtype.UUID, kind string) error {
	var customer, vendor pgtype.UUID
	var pico, total int64
	if err := pool.QueryRow(ctx,
		`SELECT customer_id, vendor_id, xmr_amount_piconero, total_usd_cents FROM orders WHERE id=$1`,
		orderID).Scan(&customer, &vendor, &pico, &total); err != nil {
		return err
	}
	var threadID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO threads (participant_a, participant_b, order_ref)
		VALUES (LEAST($1::uuid,$2::uuid), GREATEST($1::uuid,$2::uuid), $3)
		ON CONFLICT (participant_a, participant_b)
		DO UPDATE SET order_ref = COALESCE(threads.order_ref, EXCLUDED.order_ref)
		RETURNING id`, customer, vendor, orderID).Scan(&threadID); err != nil {
		return err
	}
	xmr := float64(pico) / 1e12
	short := uuidString(orderID)
	if len(short) > 8 {
		short = short[:8]
	}
	insert := func(sender pgtype.UUID, body string) error {
		_, err := pool.Exec(ctx, `INSERT INTO messages (thread_id, sender_id, body) VALUES ($1,$2,$3)`,
			threadID, sender, body)
		return err
	}
	switch kind {
	case "created":
		return insert(vendor, fmt.Sprintf(
			"Order #%s received. Awaiting payment of %.12f XMR. Open the order and use the Pay button for the address. Unpaid orders expire after 12 hours.", short, xmr))
	case "paid":
		if err := insert(vendor, fmt.Sprintf(
			"Payment for order #%s confirmed (%.12f XMR). Funds are now in escrow.", short, xmr)); err != nil {
			return err
		}
		return insert(customer, fmt.Sprintf(
			"Payment for order #%s confirmed. Funds are in escrow - please prepare and mark the order as shipped.", short))
	}
	return nil
}

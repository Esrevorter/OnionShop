package server

import (
	"log"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyOrderEvent posts audience-targeted lifecycle messages into the single
// buyer<->vendor inbox thread. audience = intended recipient (NULL = both).
func notifyOrderEvent(ctx context.Context, pool *pgxpool.Pool, orderID pgtype.UUID, event, extra string) error {
	var customer, vendor pgtype.UUID
	var pico int64
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT o.customer_id, v.user_id, o.xmr_amount_piconero, o.status
		 FROM orders o JOIN vendors v ON v.id=o.vendor_id WHERE o.id=$1`,
		orderID).Scan(&customer, &vendor, &pico, &status); err != nil {
		return err
	}
	var threadID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO threads (participant_a,participant_b,order_ref)
		VALUES (LEAST($1::uuid,$2::uuid), GREATEST($1::uuid,$2::uuid), $3)
		ON CONFLICT (participant_a,participant_b)
		DO UPDATE SET order_ref=COALESCE(threads.order_ref, EXCLUDED.order_ref)
		RETURNING id`, customer, vendor, orderID).Scan(&threadID); err != nil {
		return err
	}
	xmr := formatPiconero(pico)
	short := uuidString(orderID)
	if len(short) > 8 { short = short[:8] }
	ins := func(sender pgtype.UUID, body string, aud pgtype.UUID) error {
		var n int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages WHERE thread_id=$1 AND body LIKE $2`, threadID, body[:40]+"%").Scan(&n)
		if n > 0 { return nil }
		_, err := pool.Exec(ctx, `INSERT INTO messages (thread_id,sender_id,body,audience) VALUES ($1,$2,$3,$4)`,
			threadID, sender, body, aud)
		return err
	}
	switch event {
	case "created":
		return ins(vendor, fmt.Sprintf("Order #%s received. Awaiting payment of %s XMR. Open the order and use Pay. Unpaid orders expire after 12 hours.", short, xmr), customer)
	case "paid":
		if err := ins(vendor, fmt.Sprintf("Payment for order #%s confirmed (%s XMR). Funds are in escrow.", short, xmr), customer); err != nil { return err }
		return ins(customer, fmt.Sprintf("Payment for order #%s confirmed. Funds in escrow - please prepare and mark shipped.", short), vendor)
	case "shipped":
		body := fmt.Sprintf("Order #%s has been SHIPPED. Confirm receipt to release funds, or open a dispute if there is a problem.", short)
		if extra != "" { body += " Vendor note: " + extra }
		return ins(vendor, body, customer)
	case "extended":
		return ins(customer, fmt.Sprintf("Buyer extended escrow for order #%s by 7 days. %s", short, extra), vendor)
	case "released":
		if err := ins(vendor, fmt.Sprintf("Order #%s finalized: funds released to vendor. Thank you.", short), customer); err != nil { return err }
		return ins(customer, fmt.Sprintf("Order #%s finalized: funds released. %s", short, extra), vendor)
	case "refunded":
		return ins(vendor, fmt.Sprintf("Order #%s refunded. The XMR has been returned to the buyer.", short), customer)
	case "disputed":
		return ins(customer, fmt.Sprintf("A dispute was opened on order #%s. A moderator will review. %s", short, extra), pgtype.UUID{})
	case "overpaid":
		return ins(vendor, fmt.Sprintf("Order #%s OVERPAID. Held for manual review; do not ship yet.", short), pgtype.UUID{})
	case "underpaid":
		return ins(vendor, fmt.Sprintf("Order #%s UNDERPAID. Send the remaining amount to the same address.", short), customer)
	case "cancelled":
		return ins(vendor, fmt.Sprintf("Order #%s was cancelled (unpaid/expired or pre-shipment).", short), customer)
	}
	return nil
}

// notifyOrder keeps older call-sites working.
func notifyOrder(ctx context.Context, pool *pgxpool.Pool, orderID pgtype.UUID, kind string) error {
	return notifyOrderEvent(ctx, pool, orderID, kind, "")
}

// refundOrder returns value to the buyer on-chain (refund address) and posts the
// escrow refund ledger legs, then notifies. Idempotent via ledger keys.
func refundOrder(ctx context.Context, pool *pgxpool.Pool, provider MoneroProvider, orderID pgtype.UUID) error {
	var pico int64
	var addr string
	if err := pool.QueryRow(ctx, `SELECT xmr_amount_piconero, COALESCE(refund_address,'') FROM orders WHERE id=$1`, orderID).Scan(&pico, &addr); err != nil {
		return err
	}
	if addr != "" {
		if tx, err := provider.SendTransaction(ctx, addr, pico); err == nil {
			if _, err := pool.Exec(ctx, `UPDATE orders SET refund_tx=$1 WHERE id=$2`, tx, orderID); err != nil {
				log.Printf("server: order state write failed: %v", err)
			}
		}
	}
	_ = PostEscrowRefund(ctx, pool, orderID, pico)
	return notifyOrderEvent(ctx, pool, orderID, "refunded", "")
}

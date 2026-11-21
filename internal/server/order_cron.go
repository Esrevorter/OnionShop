package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunOrderCron checks for expired orders and transitions them.
// Awaiting payment > 12h → cancel
// In escrow, not shipped > 3 days → cancel + refund
// Shipped, auto_release_at passed → release (unless disputed)
func RunOrderCron(ctx context.Context, pool *pgxpool.Pool) error {
	now := time.Now().UTC()

	// 1. Auto-cancel awaiting_payment (12h timeout)
	rows, err := pool.Query(ctx, `
		SELECT id, customer_id FROM orders
		WHERE status = 'awaiting_payment' AND created_at < $1`,
		now.Add(-12*time.Hour))
	if err != nil {
		return fmt.Errorf("server: query awaiting_payment: %w", err)
	}
	defer rows.Close()
	var toCancel []pgtype.UUID
	for rows.Next() {
		var id, cid pgtype.UUID
		if err := rows.Scan(&id, &cid); err == nil {
			toCancel = append(toCancel, id)
		}
	}
	for _, id := range toCancel {
		_, err := pool.Exec(ctx, `UPDATE orders SET status='cancelled', cancelled_at=now(), updated_at=now() WHERE id=$1`, id)
		if err != nil {
			log.Printf("server: auto-cancel order %s failed: %v", uuidString(id), err)
			continue
		}
		// Message buyer (via direct DB insert into order_messages)
		_, _ = pool.Exec(ctx, `
			INSERT INTO order_messages (order_id, sender_type, body, created_at)
			VALUES ($1, 'system', $2, now())`, id, "Your order was cancelled (payment not received within 12 hours).")
		log.Printf("server: auto-cancelled order %s (payment timeout)", uuidString(id))
	}

	// 2. Auto-cancel in_escrow (no-ship after 3 days)
	rows2, err := pool.Query(ctx, `
		SELECT id, customer_id FROM orders
		WHERE status = 'in_escrow' AND created_at < $1`,
		now.Add(-3*24*time.Hour))
	if err != nil {
		return fmt.Errorf("server: query in_escrow: %w", err)
	}
	defer rows2.Close()
	var toRefund []pgtype.UUID
	for rows2.Next() {
		var id, cid pgtype.UUID
		if err := rows2.Scan(&id, &cid); err == nil {
			toRefund = append(toRefund, id)
		}
	}
	for _, id := range toRefund {
		_, err := pool.Exec(ctx, `UPDATE orders SET status='refunded', updated_at=now() WHERE id=$1`, id)
		if err != nil {
			log.Printf("server: auto-refund order %s failed: %v", uuidString(id), err)
			continue
		}
		// Post refund ledger entry
		var vid pgtype.UUID
		var pico int64
		if err := pool.QueryRow(ctx, `SELECT vendor_id, xmr_amount_piconero FROM orders WHERE id=$1`, id).Scan(&vid, &pico); err == nil {
			_ = PostEscrowRefund(ctx, pool, id, pico)
		}
		// Message buyer
		_, _ = pool.Exec(ctx, `
			INSERT INTO order_messages (order_id, sender_type, body, created_at)
			VALUES ($1, 'system', $2, now())`, id, "Your order was cancelled (vendor did not ship within 3 days). Refund issued.")
		log.Printf("server: auto-refunded order %s (no-ship)", uuidString(id))
	}

	// 3. Auto-release shipped (unless disputed)
	rows3, err := pool.Query(ctx, `
		SELECT id, vendor_id, xmr_amount_piconero FROM orders
		WHERE status = 'shipped' AND auto_release_at IS NOT NULL AND auto_release_at < $1`,
		now)
	if err != nil {
		return fmt.Errorf("server: query shipped: %w", err)
	}
	defer rows3.Close()
	var toRelease []struct {
		id, vid pgtype.UUID
		pico    int64
	}
	for rows3.Next() {
		var id, vid pgtype.UUID
		var pico int64
		if err := rows3.Scan(&id, &vid, &pico); err == nil {
			toRelease = append(toRelease, struct {
				id, vid pgtype.UUID
				pico    int64
			}{id, vid, pico})
		}
	}
	for _, rel := range toRelease {
		// Check for open disputes
		var disputeCount int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM disputes WHERE order_id=$1 AND resolution IS NULL`, rel.id).Scan(&disputeCount)
		if disputeCount > 0 {
			log.Printf("server: skipping auto-release for order %s (open dispute)", uuidString(rel.id))
			continue
		}
		_, err := pool.Exec(ctx, `UPDATE orders SET status='released', released_at=now(), updated_at=now() WHERE id=$1`, rel.id)
		if err != nil {
			log.Printf("server: auto-release order %s failed: %v", uuidString(rel.id), err)
			continue
		}
		_ = PostEscrowRelease(ctx, pool, rel.vid, rel.id, rel.pico)
		// Message both buyer and vendor
		msg := fmt.Sprintf("Order #%s finalized (auto-release). Funds released to vendor.", uuidString(rel.id)[:8])
		_, _ = pool.Exec(ctx, `
			INSERT INTO order_messages (order_id, sender_type, body, created_at)
			VALUES ($1, 'system', $2, now())`, rel.id, msg)
		log.Printf("server: auto-released order %s", uuidString(rel.id))
	}

	return nil
}

// StartOrderCron runs the cron every hour.
func StartOrderCron(ctx context.Context, pool *pgxpool.Pool) {
	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := RunOrderCron(ctx, pool); err != nil {
				log.Printf("server: order cron failed: %v", err)
			}
		}
	}
}

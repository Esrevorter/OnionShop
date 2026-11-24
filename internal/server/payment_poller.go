package server

import (
	"fmt"
	"os"
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StartPaymentPoller runs a background goroutine that checks for payments.
// Only active when XMR_MODE=rpc.
func StartPaymentPoller(ctx context.Context, pool *pgxpool.Pool, provider MoneroProvider) {
	if os.Getenv("XMR_MODE") != "rpc" {
		log.Printf("xmr: payment poller disabled (XMR_MODE != rpc)")
		return
	}
	log.Printf("xmr: starting payment poller (interval=30s, confirmations=%s)", 
		os.Getenv("XMR_CONFIRMATIONS_REQUIRED"))
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			log.Printf("xmr: payment poller stopped")
			return
		case <-ticker.C:
			if err := pollPayments(ctx, pool, provider); err != nil {
				log.Printf("xmr: poll error: %v", err)
			}
		}
	}
}

func pollPayments(ctx context.Context, pool *pgxpool.Pool, provider MoneroProvider) error {
	_, _ = pool.Exec(ctx, `UPDATE orders SET status='cancelled', updated_at=now() WHERE status='awaiting_payment' AND created_at <= now() - interval '12 hours'`)
	// Find orders awaiting payment
	rows, err := pool.Query(ctx, `
		SELECT id, xmr_amount_piconero, xmr_address 
		FROM orders 
		WHERE status = 'awaiting_payment' 
		AND created_at > now() - interval '12 hours'`)
	if err != nil {
		return fmt.Errorf("query awaiting orders: %w", err)
	}
	defer rows.Close()
	
	var orders []struct{
		id pgtype.UUID
		pico int64
		addr string
	}
	for rows.Next() {
		var o struct{
			id pgtype.UUID
			pico int64
			addr string
		}
		if err := rows.Scan(&o.id, &o.pico, &o.addr); err == nil {
			orders = append(orders, o)
		}
	}
	
	if len(orders) == 0 {
		return nil
	}
	
	log.Printf("xmr: checking %d orders for payment", len(orders))
	
	for _, order := range orders {
		paid, txHash, err := provider.CheckPayment(ctx, order.id, order.addr, order.pico)
		if err != nil {
			log.Printf("xmr: check payment failed for order %s: %v", uuidString(order.id), err)
			continue
		}
		if !paid {
			continue
		}
		
		// Payment confirmed! Transition via escrow state machine.
		next, err := Apply(StateAwaitingPayment, EventPaymentConfirmed)
		if err != nil {
			log.Printf("xmr: illegal transition for order %s: %v", uuidString(order.id), err)
			continue
		}
		
		// Update order status atomically
		_, err = pool.Exec(ctx, `
			UPDATE orders 
			SET status = $1, updated_at = now()
			WHERE id = $2 AND status = 'awaiting_payment'`, string(next), order.id)
		if err != nil {
			log.Printf("xmr: update order failed: %v", err)
			continue
		}
		
		log.Printf("xmr: order %s payment confirmed (tx: %s)", uuidString(order.id), txHash)
		if err := notifyOrder(ctx, pool, order.id, "paid"); err != nil {
			log.Printf("xmr: order notify(paid) failed: %v", err)
		}
	}
	
	return nil
}

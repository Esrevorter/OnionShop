package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VendorStats struct {
	OrdersByStatus       map[string]int64
	TotalOrders          int64
	GrossSalesUSDCents   int64
	UnitsSold            int64
	DistinctCustomers    int64
	RepeatCustomers      int64
	AverageOrderUSDCents int64
	ReleasedUSDCents     int64
	InEscrowUSDCents     int64
	AvgRating            float64
	ReviewCount          int64
	ActiveListings       int64
	LowStockListings     int64
	TopProducts          []VendorTopProduct
	RecentOrders         []VendorRecentOrder
	DailyViews           []VendorDailyView
}

type VendorTopProduct struct {
	Title     string
	UnitsSold int64
	Revenue   int64
}

type VendorRecentOrder struct {
	IDStr        string
	Status       string
	TotalCents   int64
	CreatedAt    string
	CustomerName string
}

type VendorDailyView struct {
	Day         string
	ShopViews   int64
	ProductView int64
}

func IncrementSiteStat(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID, metric string) {
	_, _ = pool.Exec(ctx, `
		INSERT INTO site_stats (day, vendor_id, metric, count)
		VALUES (CURRENT_DATE, $1, $2, 1)
		ON CONFLICT (day, vendor_id, metric)
		DO UPDATE SET count = site_stats.count + 1`, vendorID, metric)
}

func ComputeVendorStats(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) (*VendorStats, error) {
	s := &VendorStats{OrdersByStatus: make(map[string]int64)}

	// Orders by status + totals
	rows, err := pool.Query(ctx, `
		SELECT status, COUNT(*), SUM(total_usd_cents)
		FROM orders WHERE vendor_id=$1 GROUP BY status`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: orders by status: %w", err)
	}
	for rows.Next() {
		var status string
		var count, total int64
		if err := rows.Scan(&status, &count, &total); err == nil {
			s.OrdersByStatus[status] = count
			s.TotalOrders += count
			s.GrossSalesUSDCents += total
		}
	}
	rows.Close()

	// Units sold + distinct customers
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(oi.qty),0), COUNT(DISTINCT o.customer_id)
		FROM orders o JOIN order_items oi ON oi.order_id=o.id
		WHERE o.vendor_id=$1`, vendorID).Scan(&s.UnitsSold, &s.DistinctCustomers)
	if err != nil {
		return nil, fmt.Errorf("server: units+customers: %w", err)
	}

	// Repeat customers (customers with >1 order)
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT customer_id FROM orders WHERE vendor_id=$1
			GROUP BY customer_id HAVING COUNT(*) > 1
		) sub`, vendorID).Scan(&s.RepeatCustomers)
	if err != nil {
		return nil, fmt.Errorf("server: repeat customers: %w", err)
	}

	// Average order value
	if s.TotalOrders > 0 {
		s.AverageOrderUSDCents = s.GrossSalesUSDCents / s.TotalOrders
	}

	// Released vs in-escrow
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_usd_cents) FILTER (WHERE status='released'), 0),
		       COALESCE(SUM(total_usd_cents) FILTER (WHERE status IN ('awaiting_payment','in_escrow')), 0)
		FROM orders WHERE vendor_id=$1`, vendorID).Scan(&s.ReleasedUSDCents, &s.InEscrowUSDCents)
	if err != nil {
		return nil, fmt.Errorf("server: released+escrow: %w", err)
	}

	// Ratings
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(AVG(r.rating), 0), COUNT(r.id)
		FROM reviews r JOIN order_items oi ON oi.id=r.order_item_id
		JOIN orders o ON o.id=oi.order_id
		WHERE o.vendor_id=$1`, vendorID).Scan(&s.AvgRating, &s.ReviewCount)
	if err != nil {
		return nil, fmt.Errorf("server: ratings: %w", err)
	}

	// Listings
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status='active'),
		       COUNT(*) FILTER (WHERE status='active' AND stock < 5)
		FROM products WHERE vendor_id=$1`, vendorID).Scan(&s.ActiveListings, &s.LowStockListings)
	if err != nil {
		return nil, fmt.Errorf("server: listings: %w", err)
	}

	// Top products
	rows, err = pool.Query(ctx, `
		SELECT p.title, SUM(oi.qty), SUM(oi.unit_price_usd_cents * oi.qty)
		FROM order_items oi JOIN products p ON p.id=oi.product_id
		JOIN orders o ON o.id=oi.order_id
		WHERE o.vendor_id=$1
		GROUP BY p.id, p.title
		ORDER BY 2 DESC LIMIT 5`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: top products: %w", err)
	}
	for rows.Next() {
		var tp VendorTopProduct
		if err := rows.Scan(&tp.Title, &tp.UnitsSold, &tp.Revenue); err == nil {
			s.TopProducts = append(s.TopProducts, tp)
		}
	}
	rows.Close()

	// Recent orders
	rows, err = pool.Query(ctx, `
		SELECT o.id, o.status, o.total_usd_cents, o.created_at, u.username
		FROM orders o JOIN users u ON u.id=o.customer_id
		WHERE o.vendor_id=$1 ORDER BY o.created_at DESC LIMIT 10`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: recent orders: %w", err)
	}
	for rows.Next() {
		var ro VendorRecentOrder
		var id pgtype.UUID
		var created interface{}
		if err := rows.Scan(&id, &ro.Status, &ro.TotalCents, &created, &ro.CustomerName); err == nil {
			ro.IDStr = uuidString(id)
			cs := fmt.Sprintf("%v", created)
			if len(cs) > 16 {
				cs = cs[:16]
			}
			ro.CreatedAt = cs
			s.RecentOrders = append(s.RecentOrders, ro)
		}
	}
	rows.Close()

	// Daily views (last 30 days)
	rows, err = pool.Query(ctx, `
		SELECT day,
		       COALESCE(SUM(count) FILTER (WHERE metric='shop_views'), 0),
		       COALESCE(SUM(count) FILTER (WHERE metric='product_views'), 0)
		FROM site_stats
		WHERE vendor_id=$1 AND day >= CURRENT_DATE - INTERVAL '30 days'
		GROUP BY day ORDER BY day`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: daily views: %w", err)
	}
	for rows.Next() {
		var dv VendorDailyView
		var day time.Time
		if err := rows.Scan(&day, &dv.ShopViews, &dv.ProductView); err == nil {
			dv.Day = day.Format("01-02")
			s.DailyViews = append(s.DailyViews, dv)
		}
	}
	rows.Close()

	return s, nil
}

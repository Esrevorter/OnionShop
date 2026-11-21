package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrderItemView struct {
	Title         string
	Qty           int64
	PriceUSDCents int64
}

type OrderView struct {
	IDStr            string
	Status           string
	CustomerName     string // Populated for vendor view
	ShopName         string // Populated for customer view
	ShippingTitle    string
	XmrAddress       string
	XmrPaymentID     string
	CreatedAt        string
	TotalUSDCents    int64
	ShippingUSDCents int64
	XmrPiconero      int64
	Items            []OrderItemView
}

type OrdersData struct {
	AuthData
	Orders       []OrderView
	IsVendorView bool
}

func scanOrders(rows pgx.Rows, isVendor bool, pool *pgxpool.Pool, ctx context.Context) ([]OrderView, error) {
	defer rows.Close()
	var out []OrderView
	for rows.Next() {
		var o OrderView
		var id pgtype.UUID
		var created interface{}
		var nameOrShop string
		
		if err := rows.Scan(&id, &o.Status, &o.TotalUSDCents, &o.ShippingUSDCents, &o.XmrPiconero,
			&o.XmrAddress, &o.XmrPaymentID, &nameOrShop, &o.ShippingTitle, &created); err != nil {
			return nil, fmt.Errorf("server: scan order: %w", err)
		}
		
		if isVendor {
			o.CustomerName = nameOrShop
		} else {
			o.ShopName = nameOrShop
		}
		
		o.IDStr = uuidString(id)
		cs := fmt.Sprintf("%v", created)
		if len(cs) > 16 {
			cs = cs[:16]
		}
		o.CreatedAt = cs
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	
	for i := range out {
		oid, ok := parseUUID(out[i].IDStr)
		if !ok {
			continue
		}
		irows, err := pool.Query(ctx, `SELECT product_title, qty, unit_price_usd_cents FROM order_items WHERE order_id=$1 ORDER BY id`, oid)
		if err != nil {
			continue
		}
		for irows.Next() {
			var it OrderItemView
			if err := irows.Scan(&it.Title, &it.Qty, &it.PriceUSDCents); err == nil {
				out[i].Items = append(out[i].Items, it)
			}
		}
		irows.Close()
	}
	return out, nil
}

func ListCustomerOrders(ctx context.Context, pool *pgxpool.Pool, customerID pgtype.UUID) ([]OrderView, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, o.status, o.total_usd_cents, o.shipping_usd_cents, o.xmr_amount_piconero,
		       COALESCE(o.xmr_address,''), COALESCE(o.xmr_payment_id,''),
		       COALESCE(v.shop_name,''), COALESCE(s.title,''), o.created_at
		FROM orders o
		LEFT JOIN vendors v ON v.id = o.vendor_id
		LEFT JOIN shipping_options s ON s.id = o.shipping_option_id
		WHERE o.customer_id = $1
		ORDER BY o.created_at DESC`, customerID)
	if err != nil {
		return nil, fmt.Errorf("server: list customer orders: %w", err)
	}
	return scanOrders(rows, false, pool, ctx)
}

func ListVendorOrders(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) ([]OrderView, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, o.status, o.total_usd_cents, o.shipping_usd_cents, o.xmr_amount_piconero,
		       COALESCE(o.xmr_address,''), COALESCE(o.xmr_payment_id,''),
		       COALESCE(u.username,''), COALESCE(s.title,''), o.created_at
		FROM orders o
		LEFT JOIN users u ON u.id = o.customer_id
		LEFT JOIN shipping_options s ON s.id = o.shipping_option_id
		WHERE o.vendor_id = $1
		ORDER BY o.created_at DESC`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: list vendor orders: %w", err)
	}
	return scanOrders(rows, true, pool, ctx)
}

func (a *App) renderOrdersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	role := CurrentRole(ctx, a.sm)
	data := OrdersData{AuthData: a.baseData(r)}
	
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		if cuid, ok := parseUUID(uid); ok {
			var orders []OrderView
			var err error
			
			if role == "vendor" || role == "admin" {
				data.IsVendorView = true
				orders, err = ListVendorOrders(ctx, pool, cuid)
			} else {
				data.IsVendorView = false
				orders, err = ListCustomerOrders(ctx, pool, cuid)
			}
			
			if err != nil {
				log.Printf("server: list orders failed: %v", sanitizedErr(err))
			} else {
				data.Orders = orders
			}
		}
	}
	a.renderAs(w, "orders", http.StatusOK, data)
}

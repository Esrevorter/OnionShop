package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
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

type OrderDetailData struct {
	AuthData
	Order      OrderView
	IsVendor   bool
	CanExtend  bool
	CanRelease bool
	CanShip    bool
}

func fmtCreated(v interface{}) string {
	cs := fmt.Sprintf("%v", v)
	if len(cs) > 16 { cs = cs[:16] }
	return cs
}

func attachItems(ctx context.Context, pool *pgxpool.Pool, out []OrderView) {
	for i := range out {
		oid, ok := parseUUID(out[i].IDStr)
		if !ok { continue }
		irows, err := pool.Query(ctx, `SELECT product_title, qty, unit_price_usd_cents FROM order_items WHERE order_id=$1 ORDER BY id`, oid)
		if err != nil { continue }
		for irows.Next() {
			var it OrderItemView
			if err := irows.Scan(&it.Title, &it.Qty, &it.PriceUSDCents); err == nil {
				out[i].Items = append(out[i].Items, it)
			}
		}
		irows.Close()
	}
}

func ListCustomerOrders(ctx context.Context, pool *pgxpool.Pool, customerID pgtype.UUID) ([]OrderView, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, o.status, o.total_usd_cents, o.shipping_usd_cents, o.xmr_amount_piconero,
		       COALESCE(o.xmr_address,''), COALESCE(o.xmr_payment_id,''),
		       COALESCE(v.shop_name,''), COALESCE(s.title,''), o.created_at
		FROM orders o
		LEFT JOIN vendors v ON v.id = o.vendor_id
		LEFT JOIN shipping_options s ON s.id = o.shipping_option_id
		WHERE o.customer_id = $1 ORDER BY o.created_at DESC`, customerID)
	if err != nil { return nil, fmt.Errorf("server: list customer orders: %w", err) }
	defer rows.Close()
	var out []OrderView
	for rows.Next() {
		var o OrderView; var id pgtype.UUID; var created interface{}
		if err := rows.Scan(&id, &o.Status, &o.TotalUSDCents, &o.ShippingUSDCents, &o.XmrPiconero,
			&o.XmrAddress, &o.XmrPaymentID, &o.ShopName, &o.ShippingTitle, &created); err != nil {
			return nil, fmt.Errorf("server: scan order: %w", err)
		}
		o.IDStr, o.CreatedAt = uuidString(id), fmtCreated(created)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil { return nil, err }
	attachItems(ctx, pool, out)
	return out, nil
}

func ListVendorOrders(ctx context.Context, pool *pgxpool.Pool, userID pgtype.UUID) ([]OrderView, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, o.status, o.total_usd_cents, o.shipping_usd_cents, o.xmr_amount_piconero,
		       COALESCE(o.xmr_address,''), COALESCE(o.xmr_payment_id,''),
		       COALESCE(cu.username,''), COALESCE(s.title,''), o.created_at
		FROM orders o
		JOIN vendors v ON v.id = o.vendor_id
		LEFT JOIN users cu ON cu.id = o.customer_id
		LEFT JOIN shipping_options s ON s.id = o.shipping_option_id
		WHERE v.user_id = $1 ORDER BY o.created_at DESC`, userID)
	if err != nil { return nil, fmt.Errorf("server: list vendor orders: %w", err) }
	defer rows.Close()
	var out []OrderView
	for rows.Next() {
		var o OrderView; var id pgtype.UUID; var created interface{}
		if err := rows.Scan(&id, &o.Status, &o.TotalUSDCents, &o.ShippingUSDCents, &o.XmrPiconero,
			&o.XmrAddress, &o.XmrPaymentID, &o.CustomerName, &o.ShippingTitle, &created); err != nil {
			return nil, fmt.Errorf("server: scan vendor order: %w", err)
		}
		o.IDStr, o.CreatedAt = uuidString(id), fmtCreated(created)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil { return nil, err }
	attachItems(ctx, pool, out)
	return out, nil
}

func GetOrderDetail(ctx context.Context, pool *pgxpool.Pool, orderID, viewerID pgtype.UUID, isVendor bool) (*OrderView, error) {
	var q string
	if isVendor {
		q = `SELECT o.id, o.status, o.total_usd_cents, o.shipping_usd_cents, o.xmr_amount_piconero,
		       COALESCE(o.xmr_address,''), COALESCE(o.xmr_payment_id,''),
		       COALESCE(cu.username,''), COALESCE(s.title,''), o.created_at
		    FROM orders o JOIN vendors v ON v.id = o.vendor_id
		    LEFT JOIN users cu ON cu.id = o.customer_id
		    LEFT JOIN shipping_options s ON s.id = o.shipping_option_id
		    WHERE o.id=$1 AND v.user_id=$2`
	} else {
		q = `SELECT o.id, o.status, o.total_usd_cents, o.shipping_usd_cents, o.xmr_amount_piconero,
		       COALESCE(o.xmr_address,''), COALESCE(o.xmr_payment_id,''),
		       COALESCE(v.shop_name,''), COALESCE(s.title,''), o.created_at
		    FROM orders o LEFT JOIN vendors v ON v.id = o.vendor_id
		    LEFT JOIN shipping_options s ON s.id = o.shipping_option_id
		    WHERE o.id=$1 AND o.customer_id=$2`
	}
	var o OrderView; var id pgtype.UUID; var created interface{}
	err := pool.QueryRow(ctx, q, orderID, viewerID).Scan(&id, &o.Status, &o.TotalUSDCents, &o.ShippingUSDCents, &o.XmrPiconero,
		&o.XmrAddress, &o.XmrPaymentID, &o.CustomerName, &o.ShippingTitle, &created)
	if err != nil { return nil, err }
	o.IDStr, o.CreatedAt = uuidString(id), fmtCreated(created)
	
	irows, err := pool.Query(ctx, `SELECT product_title, qty, unit_price_usd_cents FROM order_items WHERE order_id=$1 ORDER BY id`, id)
	if err == nil {
		for irows.Next() {
			var it OrderItemView
			if err := irows.Scan(&it.Title, &it.Qty, &it.PriceUSDCents); err == nil {
				o.Items = append(o.Items, it)
			}
		}
		irows.Close()
	}
	if !isVendor { o.ShopName = o.CustomerName; o.CustomerName = "" }
	return &o, nil
}

func (a *App) renderOrdersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	role := CurrentRole(ctx, a.sm)
	data := OrdersData{AuthData: a.baseData(r)}
	if pool, ok := a.deps.Pool.(*pgxpool.Pool); ok {
		if cuid, ok := parseUUID(uid); ok {
			var orders []OrderView; var err error
			if role == "vendor" || role == "admin" {
				data.IsVendorView = true
				orders, err = ListVendorOrders(ctx, pool, cuid)
			} else {
				orders, err = ListCustomerOrders(ctx, pool, cuid)
			}
			if err != nil { log.Printf("server: list orders failed: %v", sanitizedErr(err)) } else { data.Orders = orders }
		}
	}
	a.renderAs(w, "orders", http.StatusOK, data)
}

func (a *App) handleOrderDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := CurrentUserID(ctx, a.sm)
	role := CurrentRole(ctx, a.sm)
	isVendor := role == "vendor" || role == "admin"
	
	data := OrderDetailData{AuthData: a.baseData(r), IsVendor: isVendor}
	pool, ok := a.deps.Pool.(*pgxpool.Pool)
	if !ok { writeGenericError(w); return }
	
	cuid, ok := parseUUID(uid)
	if !ok { http.Redirect(w, r, "/login", http.StatusSeeOther); return }
	
	oid, err := parseUUIDParam(r, "id")
	if err != nil { http.NotFound(w, r); return }
	
	order, err := GetOrderDetail(ctx, pool, oid, cuid, isVendor)
	if err != nil { http.NotFound(w, r); return }
	data.Order = *order
	
	if isVendor && order.Status == "in_escrow" { data.CanShip = true }
	if !isVendor && order.Status == "shipped" {
		data.CanRelease = true
		var extended bool
		_ = pool.QueryRow(ctx, `SELECT extended FROM orders WHERE id=$1`, oid).Scan(&extended)
		data.CanExtend = !extended
	}
	
	a.renderAs(w, "order_detail", http.StatusOK, data)
}

func (a *App) registerOrderRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/orders", anyUser(http.HandlerFunc(a.renderOrdersPage)))
	r.Method(http.MethodGet, "/orders/{id}", anyUser(http.HandlerFunc(a.handleOrderDetail)))
}

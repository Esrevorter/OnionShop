package server

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ShippingOption is one vendor-defined shipping method a buyer may select.
type ShippingOption struct {
	ID            pgtype.UUID
	VendorID      pgtype.UUID
	Title         string
	Description   string
	PriceUSDCents int64
	Eta           string
	EtaDays       int
	Active        bool
}

const qShippingCols = `id, vendor_id, title, description, price_usd_cents, eta, eta_days, active`

func ShippingListByVendor(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) ([]ShippingOption, error) {
	rows, err := pool.Query(ctx, `SELECT `+qShippingCols+` FROM shipping_options WHERE vendor_id=$1 ORDER BY price_usd_cents, title`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: list shipping options: %w", err)
	}
	defer rows.Close()
	var out []ShippingOption
	for rows.Next() {
		var o ShippingOption
		if err := rows.Scan(&o.ID, &o.VendorID, &o.Title, &o.Description, &o.PriceUSDCents, &o.Eta, &o.EtaDays, &o.Active); err != nil {
			return nil, fmt.Errorf("server: scan shipping option: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func ShippingListActiveByVendor(ctx context.Context, pool *pgxpool.Pool, vendorID pgtype.UUID) ([]ShippingOption, error) {
	rows, err := pool.Query(ctx, `SELECT `+qShippingCols+` FROM shipping_options WHERE vendor_id=$1 AND active ORDER BY price_usd_cents, title`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("server: list active shipping options: %w", err)
	}
	defer rows.Close()
	var out []ShippingOption
	for rows.Next() {
		var o ShippingOption
		if err := rows.Scan(&o.ID, &o.VendorID, &o.Title, &o.Description, &o.PriceUSDCents, &o.Eta, &o.EtaDays, &o.Active); err != nil {
			return nil, fmt.Errorf("server: scan shipping option: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func ShippingGet(ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) (*ShippingOption, error) {
	var o ShippingOption
	err := pool.QueryRow(ctx, `SELECT `+qShippingCols+` FROM shipping_options WHERE id=$1`, id).
		Scan(&o.ID, &o.VendorID, &o.Title, &o.Description, &o.PriceUSDCents, &o.Eta, &o.EtaDays, &o.Active)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func ShippingCreate(ctx context.Context, pool *pgxpool.Pool, o ShippingOption) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO shipping_options (vendor_id, title, description, price_usd_cents, eta, eta_days, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		o.VendorID, o.Title, o.Description, o.PriceUSDCents, o.Eta, o.EtaDays, o.Active)
	if err != nil {
		return fmt.Errorf("server: create shipping option: %w", err)
	}
	return nil
}

func ShippingToggle(ctx context.Context, pool *pgxpool.Pool, id, vendorID pgtype.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE shipping_options SET active = NOT active, updated_at = now() WHERE id=$1 AND vendor_id=$2`, id, vendorID)
	return err
}

func ShippingDelete(ctx context.Context, pool *pgxpool.Pool, id, vendorID pgtype.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM shipping_options WHERE id=$1 AND vendor_id=$2`, id, vendorID)
	return err
}

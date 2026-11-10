package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestCartOperations(t *testing.T) {
	ctx := context.Background()
	// With nil Redis, GetCart should return an empty cart without error.
	cart, err := GetCart(ctx, nil, "user-123")
	if err != nil {
		t.Fatalf("GetCart with nil rdb: %v", err)
	}
	if len(cart) != 0 {
		t.Errorf("expected empty cart, got %d items", len(cart))
	}

	// SaveCart with nil rdb should be a safe no-op.
	err = SaveCart(ctx, nil, "user-123", Cart{
		"prod-1": {ProductID: "prod-1", Title: "Test", PriceSat: 100, Qty: 2, VendorID: "v-1"},
	})
	if err != nil {
		t.Errorf("SaveCart with nil rdb error: %v", err)
	}
}

func TestParseProductFormValidation(t *testing.T) {
	app := &App{}

	// Test valid form
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{
		"title":       {"Cool Gadget"},
		"description": {"A very nice gadget"},
		"price_sat":   {"50000"},
		"stock":       {"10"},
		"status":      {"active"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = req.ParseForm()

	var dummyVendor pgtype.UUID
	_ = dummyVendor.Scan("00000000-0000-0000-0000-000000000001")

	p, msg := app.parseProductForm(req, dummyVendor)
	if msg != "" {
		t.Fatalf("unexpected validation error: %s", msg)
	}
	if p.Title != "Cool Gadget" || p.PriceSat != 50000 || p.Stock != 10 {
		t.Errorf("parsed product fields incorrect: %+v", p)
	}

	// Test invalid title (too short)
	reqBad := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{
		"title":     {"Ab"},
		"price_sat": {"50000"},
		"stock":     {"10"},
	}.Encode()))
	reqBad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = reqBad.ParseForm()

	_, msg = app.parseProductForm(reqBad, dummyVendor)
	if msg == "" {
		t.Error("expected validation error for short title, got nil")
	}
}

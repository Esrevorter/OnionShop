package server

import (
	"bytes"
	"testing"
	"time"
)

// TestRenderContract guards the recurring failure class: html/template 500s at
// runtime when a page references a field absent from its handler's data type
// (e.g. the old shop-page VendorRating bug) or calls an unregistered func.
// Executing each template against representative populated data surfaces that
// at `go test` time instead of in production.
func TestRenderContract(t *testing.T) {
	a := &App{}
	if err := a.buildTemplates("../../web/templates"); err != nil {
		t.Fatalf("buildTemplates: %v", err)
	}

	oneProduct := StorefrontProduct{
		ProductRecord:  ProductRecord{Title: "T", Description: "D", PriceSat: 10, Stock: 5, ImagePath: "/uploads/x.jpg", Status: "active"},
		ShopName:       "shop", Eta: "1d", VendorUsername: "vend",
	}
	vrec := &VendorRecord{ShopName: "shop", Approved: true}
	p := &StorefrontProduct{ProductRecord: ProductRecord{Title: "T", Description: "D", PriceSat: 10, Stock: 5, ImagePath: "/uploads/x.jpg", Status: "active"}, ShopName: "shop", VendorUsername: "vend", Eta: "1d"}

	cases := []struct{ name string; data any }{
		{"product", &CatalogData{Product: p, ShopName: "shop", SatToBTC: "0.00000010"}},
		{"catalog_home", &CatalogData{Products: []StorefrontProduct{oneProduct}, Categories: []CategoryRecord{{ID: 1, Slug: "s", Name: "S"}}}},
		{"category", &CatalogData{Products: []StorefrontProduct{oneProduct}, CategoryName: "s"}},
		{"search", &CatalogData{Products: []StorefrontProduct{oneProduct}, Query: "q"}},
		{"shop", &CatalogData{Vendor: vrec, ShopHandle: "vend", ShopName: "shop", Products: []StorefrontProduct{oneProduct}}},
		{"vendor_products", &ProductFormData{Products: []ProductRecord{{Title: "T", PriceSat: 10, Stock: 1, Status: "draft", ImagePath: "/uploads/x.jpg"}}, ShopName: "shop"}},
		{"vendor_product_edit", &ProductFormData{Editing: &ProductRecord{Title: "T", PriceSat: 10, Stock: 1, Status: "draft", ImagePath: "/uploads/x.jpg", CreatedAt: time.Now()}, ShopName: "shop"}},
	}

	for _, tc := range cases {
		tpl := a.templates[tc.name]
		if tpl == nil {
			t.Errorf("template %q not parsed", tc.name)
			continue
		}
		var buf bytes.Buffer
		if err := tpl.ExecuteTemplate(&buf, "layout", tc.data); err != nil {
			t.Errorf("render %q: %v", tc.name, err)
		}
	}
}

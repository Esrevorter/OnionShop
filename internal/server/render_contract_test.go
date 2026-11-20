package server

import (
	"bytes"
	"testing"
	"time"
)

func TestRenderContract(t *testing.T) {
	a := &App{}
	if err := a.buildTemplates("../../web/templates"); err != nil {
		t.Fatalf("buildTemplates: %v", err)
	}
	withThumb := StorefrontProduct{
		ProductRecord:  ProductRecord{Title: "T", Description: "D", PriceSat: 10, Stock: 5, ImagePath: "/uploads/x.jpg", ThumbPath: "/uploads/x_thumb.jpg", Status: "active"},
		ShopName:       "shop", Eta: "1d", VendorUsername: "vend",
	}
	noThumb := StorefrontProduct{
		ProductRecord:  ProductRecord{Title: "T2", Description: "D", PriceSat: 10, Stock: 0, ImagePath: "/uploads/y.jpg", Status: "active"},
		ShopName:       "shop", VendorUsername: "vend",
	}
	noImage := StorefrontProduct{ProductRecord: ProductRecord{Title: "T3", PriceSat: 1, Stock: 1, Status: "active"}, ShopName: "shop", VendorUsername: "vend"}
	vrec := &VendorRecord{ShopName: "shop", Approved: true}
	p := &StorefrontProduct{ProductRecord: ProductRecord{Title: "T", Description: "D", PriceSat: 10, Stock: 5, ImagePath: "/uploads/x.jpg", ThumbPath: "/uploads/x_thumb.jpg", Status: "active"}, ShopName: "shop", VendorUsername: "vend", Eta: "1d"}

	cases := []struct{ name string; data any }{
		{"product", &CatalogData{Product: p, ShopName: "shop", DisplayPrice: "0.00000010"}},
		{"catalog_home", &CatalogData{Products: []StorefrontProduct{withThumb, noThumb, noImage}, Categories: []CategoryRecord{{ID: 1, Slug: "s", Name: "S"}}}},
		{"category", &CatalogData{Products: []StorefrontProduct{withThumb}, CategoryName: "s"}},
		{"search", &CatalogData{Products: []StorefrontProduct{noThumb}, Query: "q"}},
		{"shop", &CatalogData{Vendor: vrec, ShopHandle: "vend", ShopName: "shop", Products: []StorefrontProduct{withThumb, noImage}}},
		{"vendor_products", &ProductFormData{Products: []ProductRecord{{Title: "T", PriceSat: 10, Stock: 1, Status: "draft", ImagePath: "/uploads/x.jpg", ThumbPath: "/uploads/x_thumb.jpg"}}, ShopName: "shop"}},
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

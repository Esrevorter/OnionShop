=========== gate vendor nav + graceful handlers ===========
   role field: .Role

1) gating vendor nav blocks on vendor role
   layout.html: line 63 guard -> vendor-only

2) graceful redirect for non-vendor roles
   guarded handleVendorStats in vendor_stats_handlers.go
   guarded handleShippingList in shipping_handlers.go
     vendor_stats_handlers.go: r.Method(http.MethodGet, "/vendor/stats", vendorOrAdmin(http.HandlerFunc(a.handleVendorStats)))
     shipping_handlers.go: r.Method(http.MethodGet, "/vendor/shipping", vendorOrAdmin(http.HandlerFunc(a.handleShippingList)))
     shipping_handlers.go: r.Method(http.MethodPost, "/vendor/shipping", vendorOrAdmin(http.HandlerFunc(a.handleShippingCreate)))
     shipping_handlers.go: r.Method(http.MethodPost, "/vendor/shipping/{id}/toggle", vendorOrAdmin(http.HandlerFunc(a.handleShippingToggle)))
     shipping_handlers.go: r.Method(http.MethodPost, "/vendor/shipping/{id}/delete", vendorOrAdmin(http.HandlerFunc(a.handleShippingDelete)))
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping?error=not_found", http.StatusSeeOther)
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping?error=not_found", http.StatusSeeOther)
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping", http.StatusSeeOther)
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping?error=not_found", http.StatusSeeOther)
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping?error=active_in_use", http.StatusSeeOther)
     shipping_handlers.go: http.Redirect(w, r, "/vendor/shipping?deleted=1", http.StatusSeeOther)

3) gates + deploy
   OK   go build ./...
   OK   go vet ./internal/server
   FAIL go test ./internal/server/... -count=1
 /account = 500, want 200 (body "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<meta name=\"color-scheme\" content=\"dark\">\n<title>Error 500 · OnionShop</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"/static/favicon.svg\">\n<link rel=\"stylesheet\"…")
    auth_handlers_test.go:696: captured log during failure:
        2026/11/26 01:49:13 fx: live rates updated EUR=0.912450 AUD=1.507265
        2026/11/26 01:49:13 server: render "account" failed: template: layout.html:63:16: executing "layout" at <.Role>: can't evaluate field Role in type server.AuthData
--- FAIL: TestPGPUploadStoreFailureRendersFreshChallenge (0.30s)
    pgp_test.go:287: GET /account = 500, want 200 (body "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<meta name=\"color-scheme\" content=\"dark\">\n<title>Error 500 · OnionShop</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"/static/favicon.svg\">\n<link rel=\"stylesheet\"…")
    auth_handlers_test.go:696: captured log during failure:
        2026/11/26 01:49:13 fx: live rates updated EUR=0.912450 AUD=1.507265
        2026/11/26 01:49:13 server: render "account" failed: template: layout.html:63:16: executing "layout" at <.Role>: can't evaluate field Role in type server.AuthData
FAIL
FAIL	github.com/example/onionshop/internal/server	3.568s
FAIL

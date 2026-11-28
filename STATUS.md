# STATUS.md

Working log for the current phase. Authoritative status lives in `TASKS.md` (roadmap),
`AUDIT.md` (open findings) and `PRODUCTION_READINESS.md` (deployment gates).

## 2026-11-27 — Deployment hardening & sqlc build fix (current state)

**Build/test:** `go build ./...`, `go vet ./...` and `go test ./...` are green on `main`
(verified on a dev server with Go ≥ 1.21; hosts running Go < 1.21 cannot parse the
three-part `go 1.25.0` directive in `go.mod` and must use the container toolchain instead).
The earlier `.Role` template failures in the vendor-nav guard and the five CAPTCHA-marker
failures listed below are **fixed**; those entries are kept only as history.

**sqlc/Docker build:** the app image build previously failed at `sqlc generate` because
`db/query/ledger.sql` referenced the non-existent column `balance_sat`; corrected to
`balance_piconero` (commit `d019aa7`). Bindings regenerated.

**Migration 0023 (`db/migrations/0023_ledger_hardening.up.sql`):** applies cleanly now.
It begins with `DROP VIEW IF EXISTS account_balances;` before recreating the view (PG
refuses `CREATE OR REPLACE VIEW` when a column name changes, which broke half-applied
dev databases), backfills unbalanced historical ledger rows before installing the
double-entry constraint trigger, and records versions as integers with an explicit
`dirty` value (`schema_migrations.dirty` is NOT NULL). Verified applied through version 23
on the dev server.

**deploy.py:** single-command deployment (`python3 deploy.py`) — preflight, build/test,
DB readiness via `docker compose exec -T postgres psql` (no local psql needed; works with
the read-only container rootfs), idempotent psql+`schema_migrations` migration loop
mirroring `install/install.sh`, `.env` hygiene (generates `HONEYPOT_FIELD`), compose
rebuild/restart with surfaced build errors, and `/healthz` health polling.

**Docs reconciliation:** all markdown files reviewed against code evidence; stale health
endpoint references corrected (`/healthz` is the registered route; nginx's `/health`
location proxies to it).

## Nav guard field repair
Vendor nav guard now uses the role field actually present on AuthData (taken from the pre-edit guard in HEAD), restoring /account rendering and keeping vendor links vendor-only.

## Compact UI pass
Ground truth read from origin/main before edits; captcha on one flex row at 150px, inputs 440px/7px, textarea 84px, tighter rhythm; stylesheet content-hashed.

## Captcha core remediation
Local tree reconciled against origin/main (aborted-run leftovers removed). Prompt sr-only + env-gated (CAPTCHA_TEXT_ALT=0 in production removes it from the DOM); image-left compact row with 96px answer. Follow-up deferred: spoken audio alternative for production screen-reader users.

## Historical note (superseded)
The "gate vendor nav" session record below predates the fixes above; its FAIL block was
resolved by the nav-guard field repair and friction-contract work.

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

## Nav guard field repair
Vendor nav guard now uses the role field actually present on AuthData (taken from the pre-edit guard in HEAD), restoring /account rendering and keeping vendor links vendor-only.

## Compact UI pass
Ground truth read from origin/main before edits; captcha on one flex row at 150px, inputs 440px/7px, textarea 84px, tighter rhythm; stylesheet content-hashed.

## Captcha core remediation
Local tree reconciled against origin/main (aborted-run leftovers removed). Prompt sr-only + env-gated (CAPTCHA_TEXT_ALT=0 in production removes it from the DOM); image-left compact row with 96px answer. Follow-up deferred: spoken audio alternative for production screen-reader users.

# OnionShop Comprehensive Audit Report

**Generated:** 2026-11-25 07:25:35
**Commit:** `6cb01af4ea7a`
**GitHub sync:** ✓ Verified

## Executive Summary

- **CRITICAL:** 0 (security vulnerabilities, data loss risks)
- **HIGH:** 4 (broken core functionality)
- **MEDIUM:** 1 (missing features, suboptimal UX)
- **LOW:** 2 (cosmetic, minor issues)
- **TOTAL:** 7

## 🟠 HIGH — Core Functionality Broken

### 1. Float in money path: exchange_rates.go
- **Category:** money
- **Evidence:** internal/server/exchange_rates.go uses float64/ParseFloat instead of parseDecimalUnits
- **Impact:** Rounding errors in financial calculations
- **Fix:** Replace with parseDecimalUnits or parseDecimalUnitsAllowZero

### 2. Float in money path: vendor_stats_store.go
- **Category:** money
- **Evidence:** internal/server/vendor_stats_store.go uses float64/ParseFloat instead of parseDecimalUnits
- **Impact:** Rounding errors in financial calculations
- **Fix:** Replace with parseDecimalUnits or parseDecimalUnitsAllowZero

### 3. Float in money path: catalog_store.go
- **Category:** money
- **Evidence:** internal/server/catalog_store.go uses float64/ParseFloat instead of parseDecimalUnits
- **Impact:** Rounding errors in financial calculations
- **Fix:** Replace with parseDecimalUnits or parseDecimalUnitsAllowZero

### 4. Unhealthy containers
- **Category:** infra
- **Evidence:** onionshop-monerod-1             ghcr.io/sethforprivacy/simple-monerod@sha256:d0e5c768e38870ccd22a9a07b3ebe4b2d0f12988388bab499cfa45d08273d4b1   "monerod --testnet -…"   monerod             4 hours ago      Up 4 hours (unhealthy)    18080/tcp, 18089/tcp
- **Impact:** Services not functioning correctly
- **Fix:** Check logs: docker compose logs <service>

## 🟡 MEDIUM — Missing Features / Suboptimal UX

### 1. Schema not fully migrated
- **Category:** schema
- **Evidence:** Applied: 10, Disk: 22
- **Impact:** New features may not work without migrations
- **Fix:** Run migrations 11-22

## 🟢 LOW — Cosmetic / Minor

### 1. TASKS.md references MoneroPay
- **Category:** docs
- **Evidence:** TASKS.md still mentions MoneroPay
- **Impact:** Documentation outdated
- **Fix:** Update TASKS.md to reflect in-house settlement

### 2. README.md references BTCPay
- **Category:** docs
- **Evidence:** README.md still mentions BTCPay
- **Impact:** Documentation outdated
- **Fix:** Update README.md to reflect current architecture

## Action Plan

### Immediate Actions (CRITICAL + HIGH)

- [ ] Float in money path: exchange_rates.go
- [ ] Float in money path: vendor_stats_store.go
- [ ] Float in money path: catalog_store.go
- [ ] Unhealthy containers

### Short-term Actions (MEDIUM)

- [ ] Schema not fully migrated

### Long-term Actions (LOW)

- [ ] TASKS.md references MoneroPay
- [ ] README.md references BTCPay

## Methodology

This audit established GitHub (origin/main) as ground truth and performed:
1. Static code analysis for security vulnerabilities and policy violations
2. Schema validation against migrations
3. Live application tests with authenticated sessions
4. Infrastructure health checks
5. Documentation consistency review

Findings are categorized by severity and provide exact evidence (file:line, error messages)
with actionable fixes. No iterative debugging — pure diagnosis.

## Audit Log

```
================================================================================
COMPREHENSIVE AUDIT — GitHub Ground Truth
================================================================================

[1/7] Establishing GitHub ground truth
  HEAD: 6cb01af4ea7a
  origin/main: 6cb01af4ea7a
  Sync: ✓ YES
  WARNING: Uncommitted changes present (21 files)
  Audit reflects working tree, not last commit.

[2/7] Static code analysis
  Checking for float in money paths...
  Checking friction contract compliance...
  Checking for unauthenticated routes...
  Checking for SQL injection vectors...
  Checking for panic in handlers...

[3/7] Schema validation
  Checking schema migrations...
  Checking for missing columns...

[4/7] Live application tests
  Testing admin withdrawal list...
  Testing vendor product creation...

[5/7] Infrastructure checks
  Checking app container freshness...

[6/7] Documentation consistency

[7/7] Generating comprehensive report
```
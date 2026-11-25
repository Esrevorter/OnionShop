# OnionShop — security & logic audit

| | |
|---|---|
| Generated | 2026-11-24 22:19 |
| Commit audited | `14bf57c9a368` |
| GitHub `origin/main` | `14bf57c9a368` |
| Ground truth verified | yes — tree identical to GitHub |
| Working tree | uncommitted changes present |
| Go files analysed | 62 |
| Templates analysed | 52 |
| Live DB checks | executed |
| Confirmed findings | 18 |
| Review candidates | 66 |

## Executive summary

- **CRITICAL**: 2
- **HIGH**: 7
- **MEDIUM**: 9

## Actionable items (priority order)

- [ ] **1. [CRITICAL] Image ships a committed binary** — Build from source in a multi-stage Dockerfile.
- [ ] **2. [CRITICAL] Secret file tracked in git: nginx/ssl/onion.key** — git rm --cached, rotate the secret, purge history.
- [ ] **3. [HIGH] Floating point in a money path** — Use parseDecimalUnits / integer minor units end to end.
- [ ] **4. [HIGH] Floating point in a money path** — Use parseDecimalUnits / integer minor units end to end.
- [ ] **5. [HIGH] Floating point in a money path** — Use parseDecimalUnits / integer minor units end to end.
- [ ] **6. [HIGH] CAPTCHA-gated POST but no GET mints a challenge: /messages/{{.ThreadID}}/reply** — Mint the challenge on the rendering GET, or drop the friction gate.
- [ ] **7. [HIGH] CAPTCHA-gated POST but no GET mints a challenge: /tickets/{{.Ticket.IDStr}}/reply** — Mint the challenge on the rendering GET, or drop the friction gate.
- [ ] **8. [HIGH] CAPTCHA-gated POST but no GET mints a challenge: /tickets/{{.Ticket.IDStr}}/close** — Mint the challenge on the rendering GET, or drop the friction gate.
- [ ] **9. [HIGH] Orders without a refund address** — Backfill or block checkout without it.
- [ ] **10. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **11. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **12. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **13. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **14. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **15. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **16. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **17. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.
- [ ] **18. [MEDIUM] Financial/state write with discarded error** — Check the error and RowsAffected; abort or log-and-compensate on failure.

## Confirmed findings

### 1. [CRITICAL] Image ships a committed binary
- **Category:** supply-chain
- **Evidence:** `Dockerfile`
- **Impact:** Deployed code is a stale artefact, not the audited source; patches never reach production.
- **Remediation:** Build from source in a multi-stage Dockerfile.

### 2. [CRITICAL] Secret file tracked in git: nginx/ssl/onion.key
- **Category:** secrets
- **Evidence:** `nginx/ssl/onion.key`
- **Impact:** Credentials/keys published to the remote.
- **Remediation:** git rm --cached, rotate the secret, purge history.

### 3. [HIGH] Floating point in a money path
- **Category:** money
- **Evidence:** `internal/server/vendor_fee.go:208  FeeUSD:   strconv.FormatFloat(float64(cents)/100, 'f', 2, 64),`
- **Impact:** Rounding drift breaks exact-amount payment matching and ledger balance.
- **Remediation:** Use parseDecimalUnits / integer minor units end to end.

### 4. [HIGH] Floating point in a money path
- **Category:** money
- **Evidence:** `internal/server/shipping_handlers.go:97  priceUSD, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("price_usd")), 64)`
- **Impact:** Rounding drift breaks exact-amount payment matching and ledger balance.
- **Remediation:** Use parseDecimalUnits / integer minor units end to end.

### 5. [HIGH] Floating point in a money path
- **Category:** money
- **Evidence:** `internal/server/order_notifications.go:32  xmr := float64(pico) / 1e12`
- **Impact:** Rounding drift breaks exact-amount payment matching and ledger balance.
- **Remediation:** Use parseDecimalUnits / integer minor units end to end.

### 6. [HIGH] CAPTCHA-gated POST but no GET mints a challenge: /messages/{{.ThreadID}}/reply
- **Category:** friction
- **Evidence:** `web/templates/page_message_thread.html / handler handleMessageReply`
- **Impact:** Every submission is silently swallowed (known bug class).
- **Remediation:** Mint the challenge on the rendering GET, or drop the friction gate.

### 7. [HIGH] CAPTCHA-gated POST but no GET mints a challenge: /tickets/{{.Ticket.IDStr}}/reply
- **Category:** friction
- **Evidence:** `web/templates/page_ticket_detail.html / handler handleTicketReply`
- **Impact:** Every submission is silently swallowed (known bug class).
- **Remediation:** Mint the challenge on the rendering GET, or drop the friction gate.

### 8. [HIGH] CAPTCHA-gated POST but no GET mints a challenge: /tickets/{{.Ticket.IDStr}}/close
- **Category:** friction
- **Evidence:** `web/templates/page_ticket_detail.html / handler handleTicketClose`
- **Impact:** Every submission is silently swallowed (known bug class).
- **Remediation:** Mint the challenge on the rendering GET, or drop the friction gate.

### 9. [HIGH] Orders without a refund address
- **Category:** refunds
- **Evidence:** `3`
- **Impact:** Refund path cannot return value to the buyer.
- **Remediation:** Backfill or block checkout without it.

### 10. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/dispute_handlers.go:87  _, _ = pool.Exec(ctx, 'UPDATE orders SET status='disputed', updated_at=now() WHERE id=$1 AND status IN ('in_escrow','shipped')', oid)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 11. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/checkout_handlers.go:339  _, _ = pool.Exec(ctx, 'DELETE FROM orders WHERE checkout_ref=$1', checkoutRef)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 12. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/checkout_handlers.go:357  _, _ = pool.Exec(ctx, 'DELETE FROM orders WHERE checkout_ref=$1', checkoutRef)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 13. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/checkout_handlers.go:384  _, _ = pool.Exec(ctx, 'DELETE FROM orders WHERE checkout_ref=$1', checkoutRef)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 14. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/payment_poller.go:46  _, _ = pool.Exec(ctx, 'UPDATE orders SET status='cancelled', updated_at=now() WHERE status='awaiting_payment' AND created_at <= now() - inte`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 15. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/payment_poller.go:89  _, _ = pool.Exec(ctx, 'UPDATE orders SET payment_flag='over', observed_piconero=$1 WHERE id=$2', amt, order.id)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 16. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/payment_poller.go:94  _, _ = pool.Exec(ctx, 'UPDATE orders SET payment_flag='under', observed_piconero=$1 WHERE id=$2', amt, order.id)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 17. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/withdrawal_handlers.go:160  _, _ = pool.Exec(ctx, 'UPDATE withdrawals SET tx_hash=$2 WHERE id=$1', wid, tx)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

### 18. [MEDIUM] Financial/state write with discarded error
- **Category:** integrity
- **Evidence:** `internal/server/order_notifications.go:87  _, _ = pool.Exec(ctx, 'UPDATE orders SET refund_tx=$1 WHERE id=$2', tx, orderID)`
- **Impact:** A failed UPDATE/INSERT is silently treated as success; state and ledger can diverge.
- **Remediation:** Check the error and RowsAffected; abort or log-and-compensate on failure.

## Review candidates (heuristic — needs human judgement)

### R1. [INFO] Uncommitted working-tree changes present
- **Evidence:** `M DECISIONS_REPORT.txt`
- **Why it matters:** Audit covers local state, not a published commit.
- **Suggested action:** Commit or stash, then re-run.

### R2. [HIGH] template.HTML/URL on non-literal
- **Evidence:** `internal/server/auth_handlers.go  template.HTML(strings.Join(out, " ")`
- **Why it matters:** Bypasses contextual auto-escaping.
- **Suggested action:** Escape explicitly or keep the value as plain text.

### R3. [MEDIUM] Query rows never closed in listLimit
- **Evidence:** `internal/server/catalog_handlers.go:57`
- **Why it matters:** Connection-pool exhaustion under load.
- **Suggested action:** defer rows.Close() immediately after the error check.

### R4. [MEDIUM] Query rows never closed in listOffset
- **Evidence:** `internal/server/catalog_handlers.go:77`
- **Why it matters:** Connection-pool exhaustion under load.
- **Suggested action:** defer rows.Close() immediately after the error check.

### R5. [MEDIUM] Query rows never closed in handleSearch
- **Evidence:** `internal/server/catalog_handlers.go:125`
- **Why it matters:** Connection-pool exhaustion under load.
- **Suggested action:** defer rows.Close() immediately after the error check.

### R6. [HIGH] Client IP referenced
- **Evidence:** `internal/server/headers.go:60  "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",`
- **Why it matters:** Policy forbids logging/using client IPs; also useless behind Tor.
- **Suggested action:** Rate-limit by authenticated identity; remove IP handling.

### R7. [MEDIUM] Query rows never closed in handleReviewList
- **Evidence:** `internal/server/vendor_handlers.go:377`
- **Why it matters:** Connection-pool exhaustion under load.
- **Suggested action:** defer rows.Close() immediately after the error check.

### R8. [MEDIUM] Query rows never closed in handleShippingList
- **Evidence:** `internal/server/shipping_handlers.go:31`
- **Why it matters:** Connection-pool exhaustion under load.
- **Suggested action:** defer rows.Close() immediately after the error check.

### R9. [MEDIUM] Query rows never closed in handleMessageStart
- **Evidence:** `internal/server/messaging_handlers.go:62`
- **Why it matters:** Connection-pool exhaustion under load.
- **Suggested action:** defer rows.Close() immediately after the error check.

### R10. [HIGH] SQL appears dynamically built
- **Evidence:** `internal/server/order_notifications.go:37  _ = pool.QueryRow(ctx, 'SELECT COUNT(*) FROM messages WHERE thread_id=$1 AND body LIKE $2', threadID, body[:40]+"%").Scan(&n)`
- **Why it matters:** If any operand is user-controlled this is SQL injection.
- **Suggested action:** Use parameterised queries ($1…) exclusively; never interpolate values.

### R11. [HIGH] Possible secret in log output
- **Evidence:** `cmd/onionshop/main.go:191  log.Print("SESSION_HASH_KEY unset: generating ephemeral CAPTCHA secret (dev only)")`
- **Why it matters:** Secrets in logs survive seizure and log shipping.
- **Suggested action:** Log identifiers only.

### R12. [HIGH] Object-id handler without visible ownership predicate: handleAdminSetRole
- **Evidence:** `internal/server/admin_user_admin.go:90`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R13. [HIGH] Object-id handler without visible ownership predicate: handleOrderDetail
- **Evidence:** `internal/server/orders_page.go:184`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R14. [HIGH] Object-id handler without visible ownership predicate: handleAdminForceCancel
- **Evidence:** `internal/server/admin_order_handlers.go:59`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R15. [HIGH] Object-id handler without visible ownership predicate: handleOrderShip
- **Evidence:** `internal/server/order_actions.go:26`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R16. [HIGH] Object-id handler without visible ownership predicate: handleOrderExtend
- **Evidence:** `internal/server/order_actions.go:65`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R17. [HIGH] Object-id handler without visible ownership predicate: handleOrderRelease
- **Evidence:** `internal/server/order_actions.go:94`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R18. [HIGH] Object-id handler without visible ownership predicate: handleOrderCancel
- **Evidence:** `internal/server/order_actions.go:127`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R19. [HIGH] Object-id handler without visible ownership predicate: handleAdminResetPassword
- **Evidence:** `internal/server/admin_handlers.go:290`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R20. [HIGH] Object-id handler without visible ownership predicate: handleMessageDelete
- **Evidence:** `internal/server/messaging_handlers.go:191`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R21. [HIGH] Object-id handler without visible ownership predicate: handleAdminDisputeDetail
- **Evidence:** `internal/server/admin_dispute_handlers.go:65`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R22. [HIGH] Object-id handler without visible ownership predicate: handleAdminApproveVendor
- **Evidence:** `internal/server/admin_vendor_queue.go:86`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R23. [HIGH] Object-id handler without visible ownership predicate: handleAdminRejectVendor
- **Evidence:** `internal/server/admin_vendor_queue.go:110`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R24. [HIGH] Object-id handler without visible ownership predicate: handleAdminTicketDetail
- **Evidence:** `internal/server/admin_ticket_handlers.go:53`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R25. [HIGH] Object-id handler without visible ownership predicate: handleAdminTicketReply
- **Evidence:** `internal/server/admin_ticket_handlers.go:90`
- **Why it matters:** Possible IDOR: any authenticated user may act on another's object.
- **Suggested action:** Bind every lookup to the session identity (or an explicit admin role check).

### R26. [LOW] No mem_limit on security_opt
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R27. [LOW] No pids_limit on security_opt
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R28. [LOW] No healthcheck on security_opt
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R29. [LOW] No mem_limit on tmpfs
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R30. [LOW] No pids_limit on tmpfs
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R31. [LOW] No healthcheck on tmpfs
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R32. [LOW] No mem_limit on logging
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R33. [LOW] No pids_limit on logging
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R34. [LOW] No healthcheck on logging
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R35. [INFO] Published ports on nginx
- **Evidence:** `${NGINX_BIND_IP:-127.0.0.1}:8080:8080, ${NGINX_BIND_IP:-127.0.0.1}:8443:8443`
- **Why it matters:** Expected for nginx only; verify bind address and firewall.
- **Suggested action:** Confirm NGINX_BIND_IP + Gateway-only rule.

### R36. [MEDIUM] Mutable image tag on nginx
- **Evidence:** `nginx:1.27-alpine`
- **Why it matters:** Tag can be re-pushed; builds are not reproducible.
- **Suggested action:** Pin by digest after verifying upstream.

### R37. [MEDIUM] Mutable image tag on app
- **Evidence:** `onionshop-app:local`
- **Why it matters:** Tag can be re-pushed; builds are not reproducible.
- **Suggested action:** Pin by digest after verifying upstream.

### R38. [MEDIUM] Mutable image tag on postgres
- **Evidence:** `postgres:16-alpine`
- **Why it matters:** Tag can be re-pushed; builds are not reproducible.
- **Suggested action:** Pin by digest after verifying upstream.

### R39. [MEDIUM] Mutable image tag on redis
- **Evidence:** `redis:7-alpine`
- **Why it matters:** Tag can be re-pushed; builds are not reproducible.
- **Suggested action:** Pin by digest after verifying upstream.

### R40. [MEDIUM] Mutable image tag on monero-wallet-rpc
- **Evidence:** `${WALLET_RPC_IMAGE:-ghcr.io/sethforprivacy/simple-monero-wallet-rpc:v0.18.3.4}`
- **Why it matters:** Tag can be re-pushed; builds are not reproducible.
- **Suggested action:** Pin by digest after verifying upstream.

### R41. [LOW] No healthcheck on monero-wallet-rpc
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R42. [LOW] No mem_limit on pgdata
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R43. [LOW] No pids_limit on pgdata
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R44. [LOW] No healthcheck on pgdata
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R45. [LOW] No mem_limit on backup_data
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R46. [LOW] No pids_limit on backup_data
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R47. [LOW] No healthcheck on backup_data
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R48. [LOW] No mem_limit on appsock
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R49. [LOW] No pids_limit on appsock
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R50. [LOW] No healthcheck on appsock
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R51. [LOW] No mem_limit on uploads_data
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R52. [LOW] No pids_limit on uploads_data
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R53. [LOW] No healthcheck on uploads_data
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R54. [LOW] No mem_limit on internal_net
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R55. [LOW] No pids_limit on internal_net
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R56. [LOW] No healthcheck on internal_net
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R57. [LOW] No mem_limit on edge_net
- **Evidence:** `docker-compose.yml`
- **Why it matters:** One leaking service can OOM the host.
- **Suggested action:** Set mem_limit and pids_limit.

### R58. [LOW] No pids_limit on edge_net
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Fork bomb / thread leak unbounded.
- **Suggested action:** Set pids_limit.

### R59. [LOW] No healthcheck on edge_net
- **Evidence:** `docker-compose.yml`
- **Why it matters:** Dependents may start against an unready service.
- **Suggested action:** Add a real readiness healthcheck.

### R60. [MEDIUM] Network not internal: app
- **Evidence:** `image: onionshop-app:local
    build:
      context: .
      dockerfile: Dockerfile
    <<: *default-hardening
    user: "1000:101"
    environment:
      - ONIONSHOP_UNIX=/run/onionshop/app.sock
    `
- **Why it matters:** Container egress bypasses default-deny expectations.
- **Suggested action:** Set internal: true, or document why (nginx edge needs a gateway for published ports).

### R61. [MEDIUM] Network not internal: postgres
- **Evidence:** `image: postgres:16-alpine # tag-only; verify/pin digest before production
    <<: *default-hardening
    user: "999:999"
    mem_limit: 2000m
    pids_limit: 256
    cap_add:
      - CHOWN
      - DAC`
- **Why it matters:** Container egress bypasses default-deny expectations.
- **Suggested action:** Set internal: true, or document why (nginx edge needs a gateway for published ports).

### R62. [MEDIUM] Network not internal: redis
- **Evidence:** `image: redis:7-alpine # tag-only; verify/pin digest before production
    <<: *default-hardening
    mem_limit: 512m
    pids_limit: 256
    user: "999:999"
    command: >
      redis-server
      --r`
- **Why it matters:** Container egress bypasses default-deny expectations.
- **Suggested action:** Set internal: true, or document why (nginx edge needs a gateway for published ports).

### R63. [MEDIUM] Network not internal: monerod
- **Evidence:** `entrypoint: ["monerod"]
    # TODO: REPLACE PLACEHOLDER DIGEST with actual digest from registry.
    # Run: docker pull ghcr.io/sethforprivacy/simple-monerod:v0.18.3.3
    # Run: docker inspect --form`
- **Why it matters:** Container egress bypasses default-deny expectations.
- **Suggested action:** Set internal: true, or document why (nginx edge needs a gateway for published ports).

### R64. [MEDIUM] Network not internal: backup
- **Evidence:** `build:
      context: .
      dockerfile: Dockerfile.backup
    restart: unless-stopped
    <<: *default-hardening
    mem_limit: 1500m
    pids_limit: 128
    entrypoint: ["/bin/sh", "-c", "exec /scr`
- **Why it matters:** Container egress bypasses default-deny expectations.
- **Suggested action:** Set internal: true, or document why (nginx edge needs a gateway for published ports).

### R65. [MEDIUM] Network not internal: edge_net
- **Evidence:** `driver: bridge`
- **Why it matters:** Container egress bypasses default-deny expectations.
- **Suggested action:** Set internal: true, or document why (nginx edge needs a gateway for published ports).

### R66. [INFO] App not reachable - dynamic probes skipped
- **Evidence:** `<urlopen error [Errno 111] Connection refused>`
- **Why it matters:** Runtime behaviour unverified.
- **Suggested action:** Start the stack and re-run.

## Invariant checks

| Invariant | Result | Detail |
|---|---|---|
| ledger balanced per tx_ref | PASS |  |
| no unpaid orders past expiry | PASS | count=0 |
| no shipped orders past auto-release | PASS | count=0 |
| no subaddress reuse across orders | PASS |  |
| every payable order has address+amount | PASS | count=0 |
| refund address captured | **FAIL** | count=3 |
| no negative stock | PASS | count=0 |
| paid withdrawals carry tx_hash | PASS | count=0 |
| released orders credited in ledger | PASS | count=0 |
| message audience is a participant | PASS | count=0 |
| ledger append-only trigger present | PASS | ledger_append_only |
| idempotency_key unique index | PASS |  |
| orders.status CHECK present | PASS | CHECK ((status = ANY (ARRAY['awaiting_payment'::text, 'in_escrow'::text, 'shipped'::text, 'disputed'::text, 'released':: |
| schema migrated to latest | **FAIL** | disk_max=22 applied=10 |
| escrow state machine is pure + enumerated | PASS | internal/server/escrow.go |
| payment detection is polling (no webhook) | PASS | internal/server/payment_poller.go |
| exact-amount + over/under handling | PASS | internal/server/payment_poller.go |
| ledger idempotency keys | PASS | internal/server/wallet_ledger.go |
| refunds return value on-chain | PASS | internal/server/order_notifications.go |
| buyer pre-ship cancel exists | PASS | internal/server/order_actions.go |
| ship guarded by status | PASS | internal/server/order_actions.go |
| release guarded by status | PASS | internal/server/order_actions.go |
| dispute sets disputed state | PASS | internal/server/dispute_handlers.go |
| unified lifecycle notifications | PASS | internal/server/order_notifications.go |
| audience-targeted inbox | PASS | internal/server/messaging_store.go |
| vendor fee gate enforced | PASS | internal/server/product_handlers.go |
| stock decremented atomically | PASS | internal/server/checkout_handlers.go |
| delivery details PGP-encrypted | PASS | internal/server/checkout_handlers.go |
| checkout re-validates price/stock | PASS | internal/server/checkout_handlers.go |
| withdrawal broadcasts on-chain | PASS | internal/server/withdrawal_handlers.go |
| exact decimal money parsing | PASS | internal/server/money_parse.go |
| PGP private keys rejected | PASS | internal/server/pgp.go |
| withdrawal checks balance minus pending | PASS |  |

## Runtime probes

| Probe | Result | Detail |
|---|---|---|
| GET /health reachable | **FAIL** | status=0 |
| host port closed: monero-wallet-rpc/18083 | PASS | open=False |
| host port closed: monerod/18081 | PASS | open=False |
| host port closed: postgres/5432 | PASS | open=False |
| host port closed: redis/6379 | PASS | open=False |
| certificate SAN present | PASS | subject=CN=nicey2emajmpiz6nzmraz5zj2dkualpohaxgsyirrlze63zrjlj23iad.onion X509v3 Subject Alternative Name:      DNS:nice |
| ECDSA P-384 curve | PASS | Public Key Algorithm: id-ecPublicKey |

## Build / vet / test

### go build — PASS

### go vet — PASS

### go test — PASS

## Method and limitations

- Ground truth: `git fetch origin` then compare `HEAD` to `origin/main`; the audited tree is the published tree when they match.
- Static analysis is pattern- and structure-based (function extraction, route mapping, form/CSRF/friction correlation). Heuristic items are listed separately as *review candidates* to avoid false-positive churn.
- Dynamic probes run against the local stack with certificate verification disabled (self-signed by policy). They test authentication, CSRF, headers, traversal, cookie flags and host port exposure.
- Database checks assert money and lifecycle invariants directly against live data.
- **Not covered:** cryptographic protocol review, dependency CVEs (run `govulncheck`), Tor/Whonix host firewall verification, LUKS/backup restore drills, and real Tor Browser TLS acceptance. Those remain operator gates in `PRODUCTION_READINESS.md`.

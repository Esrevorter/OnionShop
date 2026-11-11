# Project status and roadmap

`[x]` means an application feature exists, not that it is security-certified or production-operated.

## Application feature status

Phases 1–8 application features are implemented. The initial FIXME checklist's statements that these were pending were stale.

## Open engineering and production gates

- [ ] Expand high-risk escrow transition/template coverage and complete isolated integration tests.
- [ ] Verify external image digests/platforms; some compose images remain mutable tags.
- [ ] Keep Go toolchain rationale accurate: Go 1.25 is the repository's project pin, not an asserted pgx minimum.
- [ ] Review ProtonMail go-crypto dependency maintenance/security posture.
- [ ] Verify the self-signed ECDSA P-384 certificate has exact DNS SAN for actual v3 onion hostname; test exact supported Tor Browser version on clean profile. Never use a CA-issued cert, SPKI/hash pin, or warning bypass. Onion protocol provides onion identity; TLS key is separate.
- [ ] Verify Tor/host/Compose port mapping, UNIX socket permissions/health checks, service egress/DNS, and BTCPay networking.
- [ ] Complete disposable-host LUKS/firewall/systemd/Dropbear and recovery tests.
- [ ] Complete offline-key backup restore and establish canary/incident ownership.

## Phase 9: Monero Settlement Layer (MoneroPay) — supersedes BTCPay in Phase 5

> **Decision.** Replace BTCPay Server with a Monero-native settlement layer. Rationale: Bitcoin
> has no retroactive opsec — every escrow deposit, vendor payout and refund is a permanent public
> graph edge that can deanonymize the operator *and* buyers years later, after seizure or an
> exchange subpoena. Tor cannot fix this; LUKS cannot fix this. Monero removes the public graph
> entirely (unlinkable per-order subaddresses, confidential amounts, ring/FCMP sender privacy).
> Secondary rationale: BTCPay's outbound calls (rate providers, GitHub release checks, remote
> plugin builder, mempool.space explorer links) are clearnet correlation surfaces from the same
> host as the market, and its 2026 hot-wallet incidents (2.4.2 advisory Aug 7; second wave Nov 1
> against ≤2.4.1, remediation = **fresh server + new seed**, not an update) make it a poor custody
> neighbour for an onion service. See SECURITY_AND_OPSEC.md §3/§7 (to be rewritten by this phase).
>
> **Status of Phase 5 items superseded here:** "BTCPay API client + invoice-status poller",
> "Checkout flow and QR code generation", and the XMR-gating clause on "Wallets page" are
> re-opened by this phase. The cart, order-splitting and ledger primitives are retained.
>
> **Chosen backend: MoneroPay** (Go, GPL-3.0, ~96% Go — matches our stack). Subaddress-per-order,
> partial-payment summing, view-only *and* hot-wallet support, Postgres/SQLite, callbacks on
> arrival and on unlock. Alternatives considered and rejected: BTCPay Monero plugin (shares ONE
> wallet across all stores — fatal for multi-vendor); xmr-pay (MIT/Node — adds a Node runtime,
> v3 reserved for FCMP++/Carrot churn).
>
> **Architecture invariant preserved:** we keep **polling, never webhooks**. MoneroPay's callback
> feature exists but is left unused (`callback_url` empty) — the hidden service is unreachable
> inbound and a callback URL would be an egress/correlation artifact. Our goroutine polls
> `GET /receive/{address}` exactly as it polled `GET /api/v1/invoices/{id}`.

### 9.0 Remove the BTCPay placeholder (blocking — do first)
- [ ] Delete the `btcpay` and `btcpaydb` services from `docker-compose.yml`. Current state is
      non-functional and unsafe: image digest `sha256:e63283733215...f04620f4c00000000` is a
      **placeholder, not a real digest** (pull will fail); there is **no `bitcoind` and no
      NBXplorer** anywhere in the stack and no `BTCPAY_CHAINS`/`BTCPAY_BTCEXPLORERURL`, so it could
      never detect a payment; `read_only: true` with only `/tmp` tmpfs gives BTCPay nowhere to
      write keys/plugins/logs.
- [ ] Close the egress hole: `btcpay_net` is currently `driver: bridge` with **no `internal: true`**,
      and `btcpay` is dual-homed onto it. This contradicts ARCHITECTURE.md §2 and
      SECURITY_AND_OPSEC.md §5.2. Removing the services removes the hole; assert afterwards that
      **every** compose network is `internal: true` except the single deliberate node-egress path.
- [ ] Remove `BTCPAY_API_URL` / `BTCPAY_API_KEY` from the `app` environment and from
      `install/install.sh` secret generation; drop `btcpay_pgdata` from `volumes:`.
- [ ] Note: `btcpaydb` reused `${DB_USER}`/`${DB_PASSWORD}` from the main app DB — one credential
      leak crossed both databases. Do not repeat this pattern for `moneropaydb` (§9.2).

### 9.1 Own Monero node (`monerod`)
- [ ] Add `monerod` service. **Own node is mandatory** — remote nodes observe our IP, requested
      txids and timing; upstream guidance is explicit that any real business runs its own full node.
      Image: `ghcr.io/sethforprivacy/simple-monerod` pinned **by digest** (tracks upstream tags;
      current Monero release v0.18.5.4 "Fluorine Fermi"). Verify the release tarball hash against
      the GPG-signed `getmonero.org/downloads/hashes.txt` before trusting any rebuild.
- [ ] Pruned chain: `--prune-blockchain --sync-pruned-blocks` **from first sync** (adding pruning
      later only prunes logically, no disk reclaimed). Budget ~105 GB for pruned XMR plus headroom
      for chain growth; volume on the LUKS device, never tmpfs.
- [ ] Tor posture (document the tradeoff explicitly): `monerod` **cannot sync over onion/I2P** — it
      relies on IPv4 for Sybil resistance. Use `--proxy 127.0.0.1:9050 --p2p-bind-ip 127.0.0.1` to
      sync via SOCKS, plus `--tx-proxy tor,127.0.0.1:9050,10` so **our** transaction broadcasts go
      over Tor. Add `--pad-transactions` (pads relayed tx to next 1024 bytes; only meaningful behind
      Tor/I2P). Optionally `--anonymous-inbound <onion>:18084,127.0.0.1:18084,25` with a matching
      `HiddenServicePort` — note the inbound port must be unique, NOT 18080, and is not for sync.
- [ ] Hardening flags: `--no-igd --hide-my-port --no-zmq --enable-dns-blocklist
      --enforce-dns-checkpointing --check-updates=disabled --restricted-rpc`. RPC loopback only
      (`--rpc-bind-ip 127.0.0.1`), `--rpc-login user:pass`, `--disable-md5` (SHA-256 digest auth
      only). Never `--public-node`, never `--confirm-external-bind`.
- [ ] Known residual leaks to record in SECURITY_AND_OPSEC.md, not pretend away: peer timed-sync
      timestamps can link an onion address to an IPv4 address (mitigate: accurate clock); running
      the daemon only when sending is itself a fingerprint (mitigate: always-on); Tor stream reuse
      can let a hidden-service operator link two of our broadcasts. Keep the node continuously up.
- [ ] Egress isolation: the node is the **only** container with a route off-host, via a dedicated
      Tor gateway (Whonix-Gateway/AppVM split per §5.3). Every other container stays default-deny.
      Extend the §5.2 zero-DNS-egress canary to cover the node's resolver behaviour.
- [ ] Container hygiene: image entrypoint starts as root briefly then drops privileges via
      `su-exec`; UID/GID are set with **`PUID`/`PGID` env vars, not `user:`** (a `user:` override no
      longer remaps the daemon UID). Data dir `/home/monero/.bitmonero`. Compatible with
      `no-new-privileges:true` (no setuid binaries). Do not combine with `cap_drop: [ALL]` without
      testing the startup chown.

### 9.2 Wallet RPC (`monero-wallet-rpc`) — view-only
- [ ] Add `monero-wallet-rpc` service, image
      `ghcr.io/sethforprivacy/simple-monero-wallet-rpc` pinned by digest. Wallet dir
      `/home/monero/wallet`; `PUID`/`PGID` env vars as above.
- [ ] **View-only wallet.** Generate the seed on an airgapped machine; import only
      `address` + **private view key** + `restore_height` via the `generate_from_keys` RPC
      (omit `spendkey` → view-only). Spend key and seed never touch the server. Record the restore
      height so the wallet does not scan from genesis.
- [ ] Understand the limit: a view key reliably reveals **incoming** payments but **not outgoing
      spends**. It is sufficient for payment detection and insufficient for audit — our Postgres
      ledger remains the sole source of truth for balances.
- [ ] RPC lockdown: `--rpc-bind-ip 127.0.0.1`, `--rpc-login user:pass`, `--restricted-rpc`
      (view-only command set), `--trusted-daemon` (our own node only), `--disable-md5`,
      `--wallet-dir`. **Never** `--disable-rpc-login`, never `--confirm-external-bind`, never
      `RPC_EXPOSE_UNAUTHENTICATED=1`. The image binds loopback by default and only switches to
      `0.0.0.0` when `--rpc-login` is present — keep it loopback regardless.
- [ ] Publish **no ports** for this service. Docker binds published ports to `0.0.0.0` and installs
      iptables ACCEPT rules that are evaluated **before** UFW/nftables filter rules — a published
      wallet RPC can be reachable from the internet despite a default-deny firewall. Compose
      `internal: true` + no `ports:` is the control; add a CI/host assertion for it.
- [ ] Dedicated Postgres for MoneroPay (`moneropaydb`) with its **own** role and password, distinct
      from `${DB_USER}`/`${DB_PASSWORD}`. Least privilege per §6; migrations run under a separate
      short-lived role.

### 9.3 MoneroPay service
- [ ] Add `moneropay` service, image `registry.gitlab.com/moneropay/moneropay` pinned by digest
      (upstream v2.8.1). Upstream image is `FROM scratch` with a static binary and
      `ENTRYPOINT ["./moneropay", "-bind=:5000"]` — read-only-rootfs friendly.
- [ ] Configuration uses `jnovack/flag`: flags map 1:1 to env vars, uppercased, dashes→underscores.
      Set `RPC_ADDRESS=http://monero-wallet-rpc:18083/json_rpc`, `RPC_USERNAME`, `RPC_PASSWORD`,
      `POSTGRESQL=postgresql://…@moneropaydb:5432/moneropay`, `LOG_FORMAT=json`,
      `POLL_FREQUENCY=5s`. Leave `SQLITE` unset (Postgres for production).
- [ ] **`ZERO_CONF=false`.** Enabling it makes MoneroPay emit three callbacks per tx (0-conf,
      1-conf, unlock) and include mempool transfers in `GET /receive`. We do not want mempool
      noise feeding escrow. Confirmation policy lives in our state machine (§9.5), not here.
- [ ] Leave `callback_url` **empty** on every `POST /receive`. Polling stays authoritative.
      If callbacks are ever enabled as an optimisation, they must be treated as unauthenticated
      hints and re-verified by poll before any ledger write.
- [ ] Bind MoneroPay to the internal network only; no `ports:`. Its API has **no authentication
      middleware** — it sets `Server: MoneroPay/<ver>` and `X-MoneroPay-Address: <primary address>`
      on every response and exposes `POST /transfer` (spend) alongside `POST /receive`. Network
      isolation is the only access control; treat reachability as equivalent to spend authority
      (moot while the wallet is view-only, critical if a hot wallet is ever introduced).
- [ ] Strip `Server` and `X-MoneroPay-Address` at any proxy boundary; never let the primary wallet
      address leak into a browser-facing response or a log line.
- [ ] Health gate: `GET /health` returns `{status, services:{walletrpc, postgresql}}` and 503 when
      either dependency is down. Wire it into the compose healthcheck and the app's readiness gate
      so checkout refuses to create invoices against an unsynced wallet.

### 9.4 Schema & money representation (highest-risk change)
- [ ] **Rename sat-denominated columns to atomic-unit columns.** Everything is currently
      `*_sat BIGINT`: `products.price_sat`, `orders.total_sat`/`fee_sat`,
      `order_items.unit_price_sat`, `ledger_entries.amount_sat`, `withdrawals.amount_sat`,
      `payment_events.paid_amount_sat`, plus `account_balances.balance_sat`. Introduce
      `*_atomic` (or `*_pico`) naming and a `currency` column; migrate in one reviewed migration
      (`0008_monero_settlement.up/down.sql`), never a float conversion.
- [ ] **Range check.** 1 XMR = 1e12 atomic units. Total supply (~18.4M XMR ≈ 1.8e19) **exceeds
      int64 max (9.22e18)**, whereas BTC's 2.1e15 sats fits comfortably. Per-order amounts are
      safe in `BIGINT`, but add a `CHECK (amount_atomic > 0 AND amount_atomic <= < sane cap)` and
      reject overflow at the API boundary. Any aggregate over the whole ledger must use
      `NUMERIC`, not `BIGINT` SUM. Document this asymmetry — it is a genuine new failure mode
      introduced by leaving sats.
- [ ] Replace `orders.btc_address` → `payment_address TEXT` (XMR subaddress, 95 chars) and
      `orders.btcpay_invoice_id TEXT UNIQUE` → `payment_ref TEXT UNIQUE` (the subaddress is our
      poll handle; MoneroPay keys receivers by address).
- [ ] Rework `payment_events`: current uniqueness is `UNIQUE (invoice_id, btcpay_status)`. Replace
      with `UNIQUE (tx_hash)` (plus a per-order event table if multiple partial payments must be
      recorded). A tx hash is the natural idempotency key; a status string is not. Keep
      `raw_payload JSONB` but scrub it — no view keys, no seeds, no addresses beyond the order's own.
- [ ] `accounts.name` CHECK currently pins `^(escrow|platform_fee|refund|vendor_[0-9a-f-]{36})$`.
      Extend for per-currency accounts (e.g. `escrow_xmr`) or add a `currency` column; update the
      regex and the seeded rows in the same migration. `ledger_entries.idempotency_key` is already
      `UNIQUE` — reuse it with keys like `pay:<tx_hash>`, `release:<order_id>`.
- [ ] `withdrawals.btc_address` → `payout_address`, validated as a Monero address (network-aware)
      rather than free text. Preserve the §7 controls: PGP-verified pre-registration, 24–48h
      mandatory delay, 7-day hold + admin approval on any address change.
- [ ] Keep the `ledger_append_only` trigger and the `SUM(debits)=SUM(credits)` invariant untouched;
      extend the quarterly restore drill to assert them in the new units.

### 9.5 Go integration
- [ ] New `internal/server/moneropay.go` client: `CreateReceive(ctx, amountAtomic, description)`
      → `POST /receive` `{amount, description, callback_url:""}` → `{address, amount, created_at,
      description}`. `GetReceive(ctx, address, min, max)` → `GET /receive/{address}` →
      `{amount:{expected, covered:{total, unlocked}}, complete, transactions:[…], created_at}`.
      All calls take `context.Context` (AGENTS.md §2); explicit error handling, no `_ =`.
- [ ] Rewrite the poller goroutine against `GET /receive/{address}`. Backoff for aged invoices as
      today. Apply results through `NextEscrowState`/`Apply` — never write SQL before the state
      machine approves the transition.
- [ ] **Use `covered.unlocked`, not `covered.total`, and not `complete` alone.** Monero enforces a
      10-block spendable lock; `getTransferLockStatus` treats a transfer as unlocked only at
      ≥10 confirmations (or past an explicit `unlock_time`). `complete` is computed as
      `unlocked >= expected`. Escrow credit must require unlocked funds **and** our own
      confirmation threshold.
- [ ] Confirmation policy scaled by value: 1 conf for small carts, 10 for high-value; **never**
      auto-release on 0-conf. Reject `locked: true` and any nonzero `unlock_time` that has not
      elapsed as *unpaid*, not as paid. Reject `double_spend_seen: true` outright and flag for review.
- [ ] Per-transaction fields available for the audit trail: `amount, confirmations,
      double_spend_seen, fee, height, timestamp, tx_hash, unlock_time, locked`. Persist `tx_hash`,
      `height`, `confirmations`; do not persist anything key-derived.
- [ ] Partial payments: MoneroPay sums transfers per subaddress, so underpayment resolves itself
      when topped up. Keep the existing `awaiting_payment` + delta-invoice path; keep
      overpayment → manual review, never auto-release (`EventOverpaymentObserved` already returns
      the current state unchanged).
- [ ] Subaddress uniqueness: MoneroPay allocates a fresh index via `create_address` and records it
      in `subaddresses(address_index PK, address UNIQUE)` with
      `receivers(subaddress_index PK REFERENCES subaddresses ON DELETE CASCADE)`. Assert on our
      side that one order binds exactly one address and that an address is never rebound.
      **Caution:** `DELETE /receive/{address}` deletes the `subaddresses` row and cascades the
      receiver away — do not call it for unpaid/expired orders until the payment window has fully
      closed, or late payments become unreconcilable. Prefer letting our own expiry cron mark the
      order cancelled and retaining the receiver.
- [ ] Reconcile the multi-vendor split: one checkout → N vendor-scoped orders sharing one payment
      intent (ARCHITECTURE.md §5). Decide and document whether the intent gets **one** subaddress
      (simpler, single poll) or one per vendor order. One subaddress per checkout is recommended;
      the ledger, not the chain, performs the split.
- [ ] Fix the checkout stub: `handleCheckoutPost` currently hardcodes
      `InvoiceLink: "https://btcpay.example.com/invoice?id=test"` and `InvoiceID: "test-invoice-id"`.
      Replace with a real `POST /receive` call and render the address + amount locally.
- [ ] Rename `CheckoutData.TotalSat`/`InvoiceLink`/`InvoiceID` to currency-neutral fields. Render
      the XMR amount as an exact 12-decimal string from integer atomic units — never via float.

### 9.6 UI (no JS — AGENTS.md §1/§3)
- [ ] Server-rendered QR (PNG/SVG generated in-process, served from `img-src 'self'`) encoding a
      `monero:` URI with the exact `tx_amount` prefilled. No external QR service, no CDN.
- [ ] Copy-friendly address block with a visible fingerprint/first-last-chars aid; plain
      `<details>/<summary>` for wallet instructions. No clipboard JS.
- [ ] Payment status via server-rendered refresh (meta refresh or an explicit "check status" POST
      form) — no polling scripts, no SSE, no HTMX.
- [ ] Update the wallets page to show XMR available vs in-escrow from the ledger, in atomic units
      formatted once in a shared template helper.
- [ ] Buyer-facing honesty notice: state that XMR is the settlement currency, that refunds go to a
      buyer-supplied address, and (if a BTC path is ever added) that BTC is permanently linkable.

### 9.7 Documentation reconciliation (do not skip)
- [ ] ARCHITECTURE.md §2: replace the BTCPay component + "BTCPay payment notifications (no
      webhooks!)" subsection with the monerod → wallet-rpc → MoneroPay chain and the
      `GET /receive/{address}` poll contract. Keep the idempotency reasoning verbatim.
- [ ] ARCHITECTURE.md §4: escrow transitions now keyed on unlocked XMR + confirmation thresholds;
      document the 10-block consensus lock and the `unlock_time` rejection rule.
- [ ] SECURITY_AND_OPSEC.md §3: rewrite "Payment Status via Polling" for MoneroPay; §7: replace
      BTC coin-control/chain-analysis hygiene with Monero-specific rules (own node, view-only
      wallet, no address reuse across orders, no operator-controlled XMR↔BTC conversion account
      that would bridge the two graphs).
- [ ] **Fix the pre-existing TLS contradiction:** README.md and AGENTS.md §4 still mandate
      self-signed **SPKI pinning**, while SECURITY_AND_OPSEC.md §1 has been rewritten to forbid
      pinning in favour of onion-self-authenticating certificates. Pick one and align all three
      before publishing anything a user might pin. Also drop the stale `.env.example` reference in
      the compose header comment (the installer generates `.env`; no example file is checked in).
- [ ] INSTALL.md: add node sync expectations (hours-to-days, ~105 GB pruned), the airgapped
      view-key ceremony, and the "invoices unavailable until synced" gate.
- [ ] AUDIT.md: record the removed BTCPay placeholder digest, the missing bitcoind/NBXplorer, and
      the `btcpay_net` egress hole as closed findings with evidence.
- [ ] Licensing note: MoneroPay is **GPL-3.0**; OnionShop is MIT. Running it as a separate process
      over HTTP keeps us at arm's length, but if we ever vendor or link its code, review the
      copyleft implications before distributing. Record the decision either way.

### 9.8 Tests (stagenet first, always)
- [ ] Unit tests for the atomic-unit conversion helpers: exact 12-decimal round-trip, overflow
      rejection at the int64 boundary, no float anywhere in the money path.
- [ ] Unit tests for the confirmation/lock gate: locked → unpaid; `unlock_time` in the future →
      unpaid; `double_spend_seen` → reject; below-threshold confirmations → still awaiting;
      unlocked + threshold met → `in_escrow`. This is the highest-risk pure logic — extend the
      existing escrow transition suite (still open under Cross-Cutting).
- [ ] Idempotency tests: replayed `tx_hash` is an accepted no-op, never a double credit; concurrent
      duplicate observations resolve via the `UNIQUE(tx_hash)` constraint, not application luck.
- [ ] Partial/under/overpayment tests against a fake MoneroPay: sum-to-complete, delta top-up,
      overpayment flags for review without state change.
- [ ] Failure-mode tests: wallet RPC down → `/health` 503 → checkout refuses to create invoices;
      poll timeout → retryable, never a false "paid"; node unsynced → no invoice issuance.
- [ ] End-to-end on **stagenet** before any mainnet exposure: create order → pay from a second
      wallet → observe poll → escrow credit → ship → release → vendor withdrawal. Repeat for
      refund and dispute paths.
- [ ] Host assertions in CI/installer: every compose network `internal: true` except the node
      egress path; zero `ports:` on wallet-rpc/moneropay/moneropaydb; zero UDP/TCP 53 egress;
      `swapon --show` empty or LUKS-backed.

### 9.9 Optional / deferred
- [ ] BTC path, if ever required: **watch-only/descriptor wallet only** (xpub imported, spend keys
      off-server), **no Lightning ever** (LND channel keys must live on the server — that is
      precisely what was drained in the Aug 2026 incident), all outbound calls severed, version
      pinned ≥2.4.7 by real digest, and "was this box ever on ≤2.4.1?" treated as a rebuild
      trigger. Small non-escrow purchases only, with an explicit linkability warning. Never share
      a wallet, timing pattern, or conversion account between the BTC and XMR flows.
- [ ] Evaluate FCMP++/Carrot when it activates on mainnet; revisit confirmation policy and the
      `unlock_time` deprecation behaviour then. Track upstream before upgrading the node.
- [ ] Consider replacing MoneroPay with a ~500-line in-house `monero-wallet-rpc` client
      (`create_address` → `get_transfers` by `subaddr_index` → confirmations → ledger) if we ever
      want to drop the extra daemon and its unauthenticated API surface. MoneroPay is essentially
      that, already written — revisit only if the dependency becomes a liability.


See `PRODUCTION_READINESS.md`; a running local stack is not production clearance.

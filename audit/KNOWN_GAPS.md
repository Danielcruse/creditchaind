# CreditChain — Known Disclosed Gaps

This is a consolidated collection of every deliberate simplification,
limitation, or unfinished path already documented as an inline comment
throughout the codebase, plus a handful of known bugs/incidents tracked in
project notes but not yet fixed in code. Nothing here is a surprise to the
team — the point of this document is to hand an auditor a single place to
read all of it, instead of finding it scattered as doc comments across ~30
files. Each entry names the file so it can be verified directly against
source.

Entries are grouped by category. Where a source location is given, treat it
as a pointer to re-verify — line numbers drift as the code changes.

## Identity / KYC

- **"Verified" Trading Hub badge is not KYC.** (`core/company.go`,
  `core/transactions.go`, `api/companies.go`) A hub applies with free-text
  self-description (`DocumentNote` — never an uploaded/verified document); a
  registered moderator reads it and grants or denies a badge. The badge
  means "a human read this and vouched for it," nothing more. Must never be
  represented to users or regulators as real KYC/AML/licensing verification.

## Custody / Key Management

- **Server never stores a wallet's real public key beyond the current
  request.** This is a deliberate design choice (self-certifying addresses,
  see `ARCHITECTURE.md` §4), but it has real, disclosed side effects:
  - `api/explorer.go` — an address profile can't show ETHTEST/USDCTEST held
    balances for an arbitrary queried address, because deriving the
    sub-address requires the public key, which isn't on file.
  - `api/apply.go` (dormant-wallet reclaim) — the same reason means a
    dormant-wallet sweep cannot reach ETHTEST/USDCTEST balances at all.
  - `api/recovery.go` / `api/apply.go` (wallet recovery) — ETHTEST/USDCTEST
    migration during recovery only happens if the user supplies and proves
    their *old* public key; if they can't, those balances are permanently
    stranded on the frozen old address. Explicitly called out in code as "a
    real and disclosed limitation."
- **ETH custodial DeFi corner: built, but scoped to Sepolia testnet only,
  never mainnet.** (`core/eth_custodial.go`, `api/eth_signer.go`,
  `api/eth_rpc.go`, `api/eth_custodial.go`, `website/defi.html`) - a real
  AWS KMS-backed signer (secp256k1, private key material never leaves KMS)
  with a clearly-labeled local-dev-only fallback, real Sepolia transaction
  construction/signing/broadcast via go-ethereum, and a chain-ID check that
  refuses to enable at all if the configured RPC URL doesn't resolve to
  Sepolia specifically (guards against an operator mistake pointing this at
  mainnet). See `ops/ETH_CUSTODIAL_SETUP.md` for setup and every disclosed
  limitation of this first pass, most importantly:
  - **Not replicated across nodes.** A custodial account only reliably
    works from whichever node created it - deliberately not gossiped
    through the consensus pipeline, to avoid two nodes racing to broadcast
    the same real (if testnet) withdrawal transaction. See
    `api/eth_custodial.go`'s top-of-file comment.
  - **One KMS key per user**, not the shared-hot-wallet-plus-internal-
    ledger architecture real custodians use at scale - a deliberate
    simplification for a first pass proving the KMS signing pattern, not
    the final production architecture.
  - **Real Ethereum mainnet custody remains explicitly out of scope** and
    would need its own dedicated security review plus actual money-
    transmission licensing review before any code change points this at
    real value - the Sepolia-only chain-ID check exists specifically to
    make that boundary hard to cross by accident.

## Consensus / Validator Security

- **No state rollback on reorg.** `core/replace_chain.go` /
  `api/peer.go`'s `syncChainFromPeer` refuse outright if a candidate chain
  diverges from an already-applied block — there is no mechanism to roll
  back locally-applied state and adopt a competing fork past that point.
  Documented in code as "not fully BFT-robust." This is a real, live-tested
  gap: two genuine node1/node2 forks occurred during this project's testnet
  phase and both required manual operator intervention to resolve (see
  `HARDENING_HISTORY.md`).
- **Recovery/fallback block production is a deadlock-breaker, not proper
  leader election.** `api/consensus_loop.go`'s fallback path (triggered
  after `missedRoundsBeforeFallback` rounds with no block) exists because an
  earlier version of this logic caused "an unrecoverable fork" in production
  when it fired at the wrong time; the current mitigation (sync-before-
  fallback, raised threshold) is a best-effort fix for a documented past
  incident, not a formally-proven-correct recovery protocol.
- **Missed-block slashing is single-reporter-trusted.** Double-sign slash
  reports are independently re-verified from embedded cryptographic evidence
  before being applied (see `ARCHITECTURE.md` §5). Missed-block reports have
  no equivalent proof — absence can't be cryptographically demonstrated — so
  once *any* node reports a miss and it's confirmed in a block, the penalty
  applies network-wide on that one node's word. This is disclosed in code as
  a deliberate, not-yet-solved simplification (M-of-N confirmation would be
  the real fix).
- **`/peer/*` gossip endpoints are gated by one static shared secret**
  (`NETWORK_SECRET`), not per-peer identity or authorization. If unset, the
  network fails closed (unreachable) rather than falling back to any
  weaker-but-functional state — a deliberate fail-safe choice, but it means
  there is currently no path to a self-serve/permissionless join flow or any
  Sybil-resistance mechanism (see `ops/NODE_OPERATOR.md`).
- **`TxTokenMint` trusts the reporting node's own `ADMIN_TOKEN` check.**
  Other nodes accept a mint transaction on the originating node's word
  alone when applying it from a gossiped block — the same single-attestor
  trust model used for slash reports, not a separate cross-node consensus
  threshold.
- **Known bug (not yet fixed): validator min-stake mismatch.**
  `HandleRegisterValidator` (`api/validators.go`) only requires
  `StakedETH > 0` to register, but the actual leader-selection code
  (`core.isEligible`) requires stake `>= MinValidatorStake` (1,000). A
  validator can register successfully and show `"active": true` in the
  dashboard while never actually being selected as a block producer. Purely
  a UX/expectation-mismatch bug, not an exploitable security hole, but worth
  fixing before it confuses a real operator.
- **Known bug (not yet fixed): `GET /peers` always returns `[]`.**
  `api/peers.go` encodes a dead, never-updated placeholder variable instead
  of the real, live peer registry (`peerStore` in `api/node_identity.go`).
  Display-only bug — actual gossip/handshake/sync is unaffected and uses the
  correct store.

## Economic / Game-Theoretic

- **Several demo-scale timing constants stand in for real values.**
  `UnbondingPeriodSeconds` (60s, vs. a real deployment's likely days),
  `DormancyPeriodSeconds` (300s, vs. an intended ~50-year real dormancy
  period), and `MaxTestStakePerAccount` (explicitly "a shared-pilot fairness
  cap, not an economic security mechanism") all need real-world values set
  before mainnet — they exist at demo scale purely so their lifecycles could
  be exercised quickly during development.
- **Late-repayment and default reputation penalties are defined but not
  wired up.** `core/credit_engine.go` / `api/reputation_math.go` — the code
  paths for `-30` (default) and `+3` (late-but-paid) reputation deltas exist
  but require a loan due-date/lateness-detection mechanism that hasn't been
  built; only on-time repayment currently ever applies its delta. Confirmed
  explicitly disclosed in `/reputation-math` API output (`Wired: false`).
- **Reputation-delta magnitudes are judgment calls, not a locked spec.**
  E.g. `ReputationDeltaP2PTrade = 14` is documented in code as "40% above
  another reward, arbitrarily chosen," not derived from any modeled
  incentive analysis. `ReputationDeltaLoanFunded`'s continued existence is
  flagged in its own doc comment as an open question ("flag if this should
  be removed or changed").
- **`TPS` and `GasPrice` shown to users are non-functional stub values.**
  `api/network.go` hardcodes both with literal `// TODO` comments — no real
  throughput or dynamic gas calculation exists yet. Cosmetic/informational
  only; nothing depends on these values being real.
- **No atomic multi-store transaction primitive.** Every two-leg trade
  (e.g. `settleHubTradeOnChain`) is settled by explicitly reversing the
  first leg if the second fails — there is no database-style all-or-nothing
  transaction across the separate in-memory token stores. Disclosed and
  consistent everywhere it's used, but worth an auditor's attention as a
  systemic pattern rather than a one-off.
- **CCT's price against ETHTEST/USDCTEST now comes from a real order book
  (`core/order_book.go`), not an AMM.** Deliberately scoped narrowly for
  this pass — see `THREAT_MODEL.md` §3.12 for the full attack-surface
  writeup. Summarized here: no trading fee, no order expiry (orders rest
  indefinitely until filled/cancelled), no protection against wash trading
  across two wallets controlled by the same actor (only literal
  same-address self-trading is blocked), and the AMM pools for these two
  symbols are hidden from new swaps/liquidity but not deleted — they stay
  live internally for unstake-redemption crediting. An auditor should
  confirm nothing else in the codebase still treats the AMM's reserve
  ratio as authoritative pricing for these two symbols now that the order
  book is the intended source of truth.
- **The 1 CCT flat transfer fee is a disclosed rounding substitution, not
  the originally-requested amount.** `core/token_transactions.go`'s
  balance model is exclusively `uint64` with no sub-unit precision
  (confirmed via exhaustive search — no `float`/`Decimal`/`big.Float`/
  `big.Rat` type touches any token amount anywhere in the codebase), so a
  0.5 CCT fee is architecturally unrepresentable as specified. Rounded up
  to 1 CCT flat instead, applied only to CCT-denominated `TxExchange`
  transfers, swept into `cctTransferFeePoolAddress()` during transaction
  processing and paid to the block-producing validator during
  `applyBlockReward` (same block, deterministic across nodes since both
  steps happen inside `ApplyBlock`). A full system-wide precision rescale
  (e.g. moving to a smaller base unit, "milli-CCT") would allow exact
  fractional fees but wasn't undertaken as part of this change; worth
  considering before mainnet if precise fee amounts matter economically.

## Data Integrity / Replication

- **Real incident, since fixed: faucet-caused CCT supply drift.** An earlier
  version of the faucet minted tokens instantly and node-locally with no
  signature at all, which caused the two production nodes' CCT total supply
  to genuinely diverge (observed drift: 13,740 vs. 13,240 at one point). The
  underlying cause (bypassing the gossip/`ApplyBlock` pipeline) was fixed —
  faucet claims are now an ordinary signed, gossiped, block-confirmed
  transaction like any other. **The pre-existing balance drift on
  production itself was deliberately left unreconciled** — directly editing
  live wallet balances was judged riskier than fixing the code path, so the
  historical discrepancy still exists on-chain today and needs an explicit
  decision (accept it / write a one-time reconciliation transaction / other)
  before mainnet.
- **Same root cause, same fix, different feature:** `TxTokenMint`
  (admin/operator minting) had the identical "mints only on the node that
  received the HTTP call" bug, which would silently diverge derived state
  (e.g. AMM pool reserves) across nodes; fixed the same way (routed through
  the gossip/`ApplyBlock` pipeline).
- **Real incident, since fixed: two live unauthenticated-mutation
  endpoints, found via a Phase 5 systematic route audit, not a live
  exploit report.** `POST /loans/create` (`HandleCreateLoan`) took a raw,
  unsigned JSON body and wrote a `core.Loan` directly into the same
  `loanStore` the real, authenticated borrow/lend system reads from and
  pays out against — since `HandleFundLoan` verifies the *lender's*
  signature but trusts whatever `Borrower` address is already attached
  to the loan record it looks up, this was a genuine fund-misdirection
  vector (create a fake loan naming an attacker's address as borrower,
  wait for a real lender to fund it) in addition to the usual
  bypasses-gossip/causes-cross-node-divergence pattern already documented
  above. `POST /transactions` (`HandleTransaction`) built a raw
  `core.Transaction` and injected it straight into the mempool with none
  of the `Auth*` fields the signed-envelope model requires — confirmed
  this specific one could not actually mutate consensus state
  (`verifyTxAuth`'s `default` case rejects anything it doesn't recognize,
  and every case requires fields this path never sets), so its real
  impact was unauthenticated mempool/gossip spam capacity, not fund
  theft. Both were pre-authentication-era handlers (predating the signed-
  envelope model entirely) left in place after the real, authenticated
  systems (self-funded `applyBorrow`/`LoanOffer` marketplace; `recordTx`
  + `ApplyTransaction` generally) superseded them — confirmed zero
  legitimate callers (no website JS, no ops tooling) before removing both
  routes and their handler functions outright. See
  `HARDENING_HISTORY.md` §7 for the full writeup, including everything
  else the same audit pass checked and confirmed was *not* a gap.
- **Swap-based reward crediting falls back to a guaranteed 1:1 rate only
  when the pool can't be quoted.** The normal path returns real pool pricing
  (including fees/slippage) — this is a disclosed pricing-risk simplification
  for the fallback case, not a guaranteed-return promise in general.

## Recovery / Account Security

- **Wallet recovery is a testnet-only convenience, explicitly not real
  account-recovery infrastructure.** (`core/recovery.go`,
  `api/recovery.go`) It does not and cannot recover a lost private key — it
  migrates balances to a *new* wallet, gated only by a low-entropy "wallet
  creation year" security question plus a verified email address. The code's
  own doc comment states this security model is "deliberately weaker than a
  signature" and must be treated as a testnet convenience only.
- **No real email service is wired up.** `api/recovery.go` — the one-time
  verification code for both the "set recovery email" and "recover wallet"
  flows is returned directly in the HTTP response body (with an explicit
  "TESTNET MODE" label), not actually emailed anywhere. This must be replaced
  with a real SMTP/SendGrid-style integration before any real user relies on
  email recovery.
- **The recovery verification code itself is not part of the consensus/
  security boundary** — it's described in code as playing the same role as
  a CAPTCHA (bot-deterrence), not as cryptographic authentication.

## Moderation / Trust Model

- **Moderator attestation is human trust, not cryptographic verification.**
  (`core/moderator.go`, `core/p2p_offer.go`, `core/negotiation.go`,
  `api/p2p_external.go`, `api/moderators.go`) — consistently and repeatedly
  disclosed in code: a moderator confirming a trade or resolving a dispute
  is a documented judgment call about whether an off-app (real-world)
  payment happened, never a cryptographic guarantee. Moderator oversight
  never gates or reverses the underlying on-chain fund movement — it's an
  attestation layer sitting alongside the economic settlement, not an
  escrow-release mechanism in the traditional sense.
- **A trade can go permanently unassigned if zero moderators are
  registered.** `core.ModeratorStore.AssignNext` returns `""` in that case —
  disclosed as a real gap on a brand-new/small network, not an edge case
  that's been engineered around.

## Networking / P2P Permissioning

- **Permissioned network, no Sybil resistance, no self-serve join.** Already
  covered above under Consensus — listed again here because it's as much a
  networking-model decision as a consensus one. See `ops/NODE_OPERATOR.md`
  for the current manual-handout join process.
- **Platform fee mechanism fails open (no fee), not closed (error), if
  misconfigured.** If `PLATFORM_FEE_ADDRESS` is unset, `applyPlatformFee`/
  `skimPlatformFee` silently skip taking a fee rather than erroring — a
  deliberate choice to never block a legitimate trade over an ops
  misconfiguration, but worth an auditor confirming that's the intended
  failure mode for a revenue-bearing mechanism.
- **Admin endpoints are fully disabled by default, not gated-but-open.** If
  `ADMIN_TOKEN` is unset, admin routes fail closed entirely — meaning
  there's no admin auth configured unless an operator explicitly opts in by
  setting the token. Confirm this is set (and rotated) appropriately in the
  real production environment.

## Other

- **Hub storefronts render genuinely untrusted third-party HTML/CSS/JS.**
  (`core/hub_storefront.go`) The entire safety of this feature rests on the
  sandboxed-iframe isolation (`sandbox="allow-scripts"` only, deliberately
  *without* `allow-same-origin`) implemented in
  `website/js/p2p.js`'s `renderStorefrontFrame` — this must never be
  rendered any other way. Worth an auditor's explicit sign-off given it's
  the one place in the app that intentionally executes arbitrary
  user-supplied code.
- **QR code encoder was verified structurally, not via an actual scan
  test.** `website/js/qrcode.js` was checked by inspecting canvas pixel
  output against the expected encoding pattern, not by scanning the
  generated code with a real QR reader. Low risk (display-only, not a
  security boundary) but worth a quick manual scan-test before shipping to
  real users who'll rely on it to receive funds.

## Infrastructure / Operational (from this session's work, not yet in code
comments)

- **Backup script's AWS calls authenticate via an unexpected identity.**
  During Phase 0 (off-server encrypted backups) verification, debug output
  showed the `aws` CLI calls from node1 authenticating with a temporary
  `ASIA...`-prefixed session credential rather than the long-lived `AKIA...`
  backup-only IAM user configured in `/etc/creditchain-backup.env`. The
  actual backup uploads are confirmed working and the data is confirmed
  safely persisted in S3 — this is not a data-loss risk — but it means the
  real credential boundary in effect on that server isn't fully understood
  yet (most likely an IAM role attached directly to the Lightsail instance,
  unconfirmed). Worth resolving before an infra-focused portion of a review,
  since "an unexplained second identity has AWS access" is exactly the kind
  of thing an auditor will flag.
- **Single-JSON-file persistence, no real database — Postgres migration
  still not wired up, but the capacity blocker that ruled it out is now
  resolved.** `state.json` / `blockchain.json` are still rewritten
  wholesale on every save and remain the sole *authoritative* source a
  node reads from at startup — no transactional guarantees, unbounded
  growth, single-write-can-corrupt-everything risk on that primary copy is
  still real. **Dual-write code exists and is ready** (`api/db.go`,
  `ops/POSTGRES_SETUP.md`) — every save additionally writes the same
  snapshot into Postgres if `DATABASE_URL` is configured — this was
  deliberately not wired up when node1 was on the smallest Lightsail RAM
  tier (see the resolved memory-pressure entry below), but node1 has since
  been resized to 4GB RAM with real headroom, so that specific blocker no
  longer applies. Wiring up an actual RDS instance (or confirming
  self-hosting Postgres on the now-larger box is safe) remains a real
  to-do, just no longer blocked on capacity. Decomposing individual stores
  into a real relational schema (the deeper part of this gap) remains
  unstarted regardless.
- **RESOLVED this session: node1's memory pressure.** Node1 was found
  running on the smallest Lightsail tier (911MB total, 47Mi free, 489Mi in
  swap) — the same condition that caused node2's earlier OOM/fork cycle.
  Fixed via a proper resize: snapshot → new 4GB/2vCPU/80GB instance →
  careful stop-old/resync-final-state/start-new sequence (mirroring the
  same non-destructive procedure used for node2's fork recovery) → static
  IP moved to the new instance, confirmed zero data loss (matching block
  height/hash throughout) and zero DNS change needed. Verified post-move:
  `/proc/meminfo` shows 3.9GB total, ~2.3GB available. One real hazard hit
  and worth flagging for any future repeat of this procedure: the
  snapshot's Plesk-blueprint origin means each new instance created from
  it ships with a host-level iptables firewall (`plesk-firewall.service`,
  default-DROP policy) that blocks the app's port 8080 even after
  Lightsail's own cloud firewall allows it — this needs a manual
  `iptables -I INPUT 1 -p tcp --dport 8080 -j ACCEPT` (and the matching
  Lightsail cloud-firewall rule) on every new instance created this way,
  not just the first time. The old smaller instance (still Plesk-blueprint
  origin, previously the live one) was left stopped/unattached rather than
  deleted, at the user's choice, as a spare.
- **Monitoring/alerting: health-check deployed and running on both
  production nodes** (`ops/healthcheck.sh` + systemd timer, every 2
  minutes) — checks process-alive, disk usage, block-height stall, and
  cross-node hash divergence (the exact failure mode behind both real
  production forks — see `HARDENING_HISTORY.md`). Verified working both
  locally (simulated stall, matching-peer no-false-positive, unreachable-
  peer cases) and on both live production nodes (first run on each reported
  clean, with real block height/hash) — confirmed still active on node1
  this session (`creditchain-healthcheck.timer`/`creditchain-backup.timer`
  both `active`). **External heartbeat monitor (healthchecks.io /
  UptimeRobot) and CDN/WAF (Cloudflare) were explicitly decided against
  for this round**, not merely deferred by oversight — both require
  creating a third-party account, which is outside what this assistant
  will do on a user's behalf regardless of how low-friction the signup is;
  the user was given exact setup steps and declined to act on them this
  session. Until either is set up, a failure is only visible via
  `journalctl` — nothing pages a human automatically, and there's no
  CDN/WAF layer in front of the public domain.
- **Node2 (13.40.131.231) has a real history of instability and is
  currently unreachable/out of scope again as of this session.** Prior
  incidents on this same box, in order: (1) a total blackout (SSH/HTTPS/
  ping all timing out) traced to the instance itself hanging, resolved by
  a console restart; (2) after that restart, a genuine chain fork at block
  index 46856 (same "reorg-with-rollback not supported" failure class as
  two earlier production forks); (3) the root economic cause of the
  hang/fork cycle: both production nodes run on the smallest Lightsail RAM
  tier (911MB total), and node2's in-app P2P sync path was observed being
  repeatedly OOM-killed mid-sync while trying to catch up to node1's
  full chain over HTTP — `syncChainFromPeer`'s `SaveState()` only fires
  once, after its entire per-block apply loop completes, so every OOM
  crash mid-sync wasted the whole attempt and restarted from zero,
  meaning the built-in sync could plausibly never complete on its own at
  this chain size on this RAM tier. That specific incident was resolved by
  directly staging node1's `state.json`/`blockchain.json` onto node2
  (non-destructively, old files renamed not deleted) rather than relying
  on the OOM-prone sync path, and node2 caught up and ran normally
  afterward. **Node2 has since gone unreachable again** and, per the
  user's explicit instruction, is being left alone rather than
  investigated/recovered as part of ordinary session work — meaning the
  order-book deploy in this update went to node1 only, and node2's chain
  state is now stale relative to node1's by however long it's been down.
  Before any future two-node work, this needs (a) confirming node2's
  actual state when it's manually brought back, since a large amount of
  new consensus-relevant code (the order book, CCT fees) shipped to node1
  alone in the meantime, and (b) revisiting the underlying RAM-tier
  capacity issue rather than treating each recurrence as a one-off, since
  the same OOM/fork failure mode has now recurred multiple times on the
  same undersized instance.

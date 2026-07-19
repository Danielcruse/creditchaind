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
- **Multi-Chain HD Wallet (Phase A): hand-implemented crypto primitives,
  not a vendored/audited library.** (`website/js/vendor/hd-crypto.js`,
  `website/js/vendor/bip39-wordlist.js`, `website/js/hdwallet.js`) One
  recovery phrase now derives a CreditChain (SLIP-10 Ed25519), Ethereum
  (BIP-32 secp256k1, testnet), and Bitcoin (BIP-32 secp256k1, BIP-84
  testnet bech32) account. Vendoring an audited library (`@scure/bip39`
  + `@scure/bip32` + `@noble/curves`, per the original design) was
  blocked - no download capability, no Node/npm in the build
  environment - so secp256k1 point arithmetic, Keccak-256, RIPEMD-160,
  and Bech32 are hand-implemented instead (CreditChain's own Ed25519
  path only needs HMAC-SHA512 chaining plus native WebCrypto). Every
  primitive was cross-checked against an independent Go reference
  implementation (`cmd/hdoracle`, built only from dependencies already
  shipped in production - `golang.org/x/crypto`, go-ethereum's own
  secp256k1 curve) against known BIP-32/BIP-39 test vectors, including
  a full real Ed25519 signing round-trip. The bundled BIP-39 English
  wordlist was hand-typed (same download restriction) and has since
  been diffed word-for-word against the authoritative copy at
  `github.com/bitcoin/bips/blob/master/bip-0039/english.txt` - zero
  differences across all 2048 entries, matching SHA-256 hashes. Treat
  this exception as scoped narrowly to HD derivation math and address
  encoding; it does not extend to any other crypto in this codebase.
- **Ethereum/Bitcoin accounts in the HD wallet were receive-only in
  Phase A; Multi-Chain HD Wallet Phase B made the self-custodial
  Ethereum account able to really send/receive real (testnet) Sepolia
  ETH — Bitcoin remains receive-only, unchanged.** (`website/js/hdwallet.js`,
  `website/asset.html`, `website/send-eth.html`) Bitcoin still has no
  balance lookup, transaction construction, or broadcast - no RPC
  wiring exists for it yet. This is architecturally separate from the
  existing KMS-custodial Sepolia DeFi corner above (different trust
  model: user-held keys, not server-held) and is not related to it
  beyond both touching Ethereum.
- **Multi-Chain HD Wallet Phase B: hand-implemented RFC 6979
  deterministic ECDSA signing + RLP encoding, not a vendored/audited
  library — same disclosed exception class as Phase A's derivation
  math, now extended to transaction signing.** (`website/js/vendor/eth-tx.js`)
  Gives the self-custodial Ethereum account (private key held only in
  the browser, never sent to the server) the ability to sign and
  broadcast a real legacy Sepolia transaction: RFC 6979 deterministic
  nonce generation (no randomness, so a weak/predictable/reused
  per-signature `k` — the exact vulnerability class that leaked Sony's
  PS3 signing key — can't occur from a bad RNG), r/s ECDSA math done
  mod the curve order (`SECP256K1_N`, never the field prime used for
  point coordinates), EIP-2 low-S normalization, and a hand-rolled RLP
  encoder. Verified via the same oracle-cross-check methodology as
  Phase A's derivation math: a throwaway Go reference tool
  (`cmd/ethsignoracle`, built only from go-ethereum functions this
  project's own KMS-custodial signer already trusts in production -
  `crypto.Sign`, `types.NewTx`, `types.LatestSignerForChainID`,
  `tx.WithSignature`) produced 4 fixed test vectors (including a
  zero-value transfer and a `gasPrice=128` value sitting exactly on
  the RLP single-byte/length-prefixed encoding boundary), each
  independently re-verified via `crypto.Ecrecover` to confirm the
  signature genuinely recovers to the known sender - not just "the
  numbers matched." The JS implementation's output was byte-for-byte
  identical to the oracle's raw signed transaction hex on all 4
  vectors. `cmd/ethsignoracle` is deleted once no longer needed for
  further spot-checks, same lifecycle as the deleted `cmd/hdoracle`.
  **`/eth-selfcustody/broadcast` deliberately has no CreditChain
  request-signature auth** (`requireSignedRequest` verifies a
  CreditChain Ed25519 envelope tied to a CreditChain address, which
  has no relationship to the Ethereum address that actually signed
  the transaction being relayed) - the real authorization boundary is
  Ethereum's own signature verification, independently re-checked by
  every Sepolia node the transaction reaches; an attacker without the
  private key can only submit garbage that fails RLP-decode or the
  network's own check. Residual spam/abuse risk is covered by the
  existing global per-IP rate limiter, same as every other route.
  **Read path (balance/nonce/gas-price) has since been verified
  against real live Sepolia infrastructure** - `SEPOLIA_RPC_URL` wired
  to a public keyless Sepolia RPC endpoint, chain-ID-verified by
  `InitEthRPC` same as production, `/eth-selfcustody/balance` returns
  genuine on-chain data (confirmed nonce/balance/live gas price for
  the real derived test address). **The write path (an actual signed
  broadcast reaching real Sepolia) remains unverified against a live
  network** - the user explicitly declined to fund a throwaway test
  account via a faucet, so this specific step is accepted as an
  intentional, disclosed risk rather than a completed check: the
  broadcast code has only ever been proven correct via the oracle
  byte-for-byte match (§ above) and a from-the-real-page signing dry
  run, never a live send. First real signed Sepolia transaction from
  this code, whenever it happens, is the actual first live-fire test.
- **The spec's "USDT (CCC1)" has no backend equivalent.** `USDCTEST`
  (`core/address.go`'s `USDCTestAddressTag`) is CreditChain's existing
  imported-test-USDC token, not USDT - Phase A does not rename or
  repurpose it as a USDT stand-in. No USDT row/asset exists anywhere
  in the app.
- **Legacy-wallet migration is user-driven, not automatic.** (`api/apply.go`'s
  `applyHDWalletMigrate`, `website/js/hd_migrate.js`) A self-custodial
  wallet can't be silently upgraded server-side (the server never has
  a private key to derive a new mnemonic from) - every pre-Phase-A
  wallet is prompted on login and must explicitly walk through
  migration, signed by its own still-controlled old key. Until a user
  does this, their wallet keeps working exactly as before, just
  without ETH/BTC accounts.
- **Auto-bridge feature: deposit detection inherits the ETH custodial
  corner's non-gossip limitation.** (`api/eth_bridge.go`'s
  `startEthBridgeDepositLoop`/`pollAndReportBridgeDeposit`) Deposit
  polling only runs against `ethCustodialStore.Snapshot()` - the same
  node-local, not-replicated store documented above under "Not
  replicated across nodes." A custodial account's real Sepolia balance
  is therefore only ever polled and reported by the one node that
  created that account; other nodes never independently notice a
  deposit. Once a report *is* submitted, though, it's an ordinary
  signed `TxBridgeDeposit` that gossips and confirms like any other
  transaction (reusing the exact `TxSlashReport`
  single-node-attestation pattern - see `applyBridgeDeposit`'s doc
  comment), so every node converges on the same ETHB credit once
  confirmed. The gap is detection-side only, not settlement-side.
- **Auto-bridge feature: ERC-network withdrawal's ETHB debit and its
  real Sepolia broadcast are two separate, non-atomic steps.**
  (`api/eth_bridge_withdraw.go`'s `HandleBridgedEthWithdraw`) Same
  disclosed risk class as the ETH custodial corner's own withdrawal
  path above, now extended to the bridge: the handler records the
  ETHB debit through the normal signed-tx/block-confirmation pipeline,
  then - deliberately not waiting for that confirmation - broadcasts
  the real Sepolia transaction synchronously on the same node. A crash
  between those two steps leaves a debited-but-not-broadcast state
  (recoverable - nothing was actually sent); a crash or non-confirmation
  of the debit after a successful broadcast leaves a
  broadcast-but-not-debited state (not automatically recoverable). A
  node-local, best-effort reservation guard (`bridgeWithdrawReserved`)
  closes the more urgent double-broadcast race (two rapid requests
  passing the same stale balance check), but this single-request
  crash-timing edge case is accepted as a disclosed, testnet-scoped
  simplification, not solved - matching this project's own precedent
  at `eth_custodial.go` for why a two-phase-commit/outbox pattern is
  judged disproportionate here.

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
- **RESOLVED: validator min-stake mismatch.** `HandleRegisterValidator`
  (`api/validators.go`) now rejects registration below `core.MinValidatorStake`
  (1,000) with a clear error, matching the actual leader-selection threshold
  (`core.isEligible`) exactly — a validator can no longer register
  successfully and show `"active": true` while never actually being
  eligible to produce. Re-verified directly against source this session
  (this entry previously said "not yet fixed" — that was stale).
- **RESOLVED: `GET /peers` always returned `[]`.** `api/peers.go` now
  encodes `peerStore.List()`, the real live peer registry (`api/
  node_identity.go`), not the old dead placeholder variable. Re-verified
  directly against source this session (this entry previously said "not yet
  fixed" — that was stale).

## Proof of Stake with Reputation (PoSR)

Formalizes CreditChain's consensus model per the approved spec (2026-07):
validation rights require Economic Stake (CCT) + Protocol Trust (Trust
Score), not wealth alone. Most of the selection/reputation machinery
already existed from earlier work (`core/consensus.go`'s weighted
stake/reputation/uptime leader selection, continuous Reputation Credit
mining — see the Reputation/Mining section above); this pass closed the
remaining gaps: a real correctness bug in validator eligibility gating,
literal stake slashing, a permanent-ban mechanism, a decaying block
reward, and a genesis token allocation.

- **RESOLVED, deeper than the earlier "min-stake mismatch" entry above:
  validator eligibility was gated on `Wallet.StakedETH` (ETHTEST), not
  CCT.** Confirmed via `core/staking_sync.go`'s own doc comment
  ("predates multi-token staking"). `api/apply.go`'s
  `applyValidatorRegister` — the actual consensus-applied check, not
  just the HTTP pre-check — now requires `wallet.StakedCCT >=
  core.MinValidatorStake`, matching PoSR's literal "Stake the minimum
  required amount of CCT." `api/consensus_loop.go`'s `hydrateValidator`
  (the function that actually feeds `core.Validator.Stake` into
  selection weight) was updated the same way, so registration
  eligibility and selection weight are now consistent with each other
  and with the spec. ETHTEST staking's own yield/unbonding mechanics are
  completely unaffected — only the validator-eligibility *read* moved.
- **Stake-slash and permanent-ban magnitudes are judgment calls, not
  specified numbers.** `core/slashing.go`'s `MissedBlockStakeSlashBps`
  (0.50%) and `DoubleSignStakeSlashBps` (10.00%) are sized proportional
  to the existing, already-locked reputation penalties (2 vs. 50 points)
  but weren't independently derived from an economic model.
  `PermanentBanSlashCountThreshold` (6, reusing the existing strike
  weights — roughly 2 double-signs, or 1 double-sign + 3 missed blocks)
  is the same kind of judgment call. Slashed stake is burned (removed
  from `TokenStore.TotalSupply` via `stakeEscrowAddress`), never
  redistributed to the reporting node — a deliberate choice to avoid
  incentivizing false reports for profit.
- **The controlled-inflation block reward schedule applies a *relative*
  decay curve to the existing real reward, not the spec's literal
  decimal numbers.** The spec's example (1.00/0.80/0.60/0.45/0.30 CCT)
  uses fractional CCT, which this ledger's whole-unit `uint64` amounts
  don't support anywhere (adding fractional-CCT support would be a much
  larger, separate migration touching every balance/fee in the system).
  `api/tokenomics.go`'s `blockRewardSchedule` instead applies the same
  1.00x/0.80x/0.60x/0.45x/0.30x curve to the already-live
  `ProduceBlockReward` (10 CCT) as the Year-1 baseline: 10 → 8 → 6 → 5 →
  3 CCT, held flat at the Year-5 floor thereafter (the spec only defines
  5 years). Anchored to the chain's real genesis block timestamp
  (`blockchain.Blocks[0].Timestamp`), never wall-clock.
- **Genesis pool allocation is a one-time, explicitly-triggered admin
  action, not a true genesis-block mechanism.** This chain has been
  running for a long time already (148,000+ blocks at the time this was
  built), so there's no true "genesis" moment to hook into
  programmatically. `POST /admin/genesis-pools/allocate`
  (`api/tokenomics.go`) mints 1,000,000,000 CCT once, split 40% Validator
  Reward Pool / 25% Foundation Treasury / 15% Ecosystem Development / 10%
  Governance Treasury / 10% Community Incentives (locked amounts, per
  explicit user approval 2026-07) — a slice of the new
  `MaxTotalCCTSupply` cap (5,000,000,000), not the whole cap, leaving
  ~4B headroom for the block-reward schedule above to mint into over
  subsequent years. Guarded idempotent via
  `core.Treasury.GenesisPoolsAllocated` so it can only ever fire once.
- **4 of the 5 genesis pools have no controlling keypair and are not
  spendable via an ordinary wallet-signed transaction.** Validator
  Reward Pool, Ecosystem Development, Governance Treasury, and Community
  Incentives live at synthetic addresses (`"POOL:*"`, same convention as
  `STAKE:*`/`FEE:*` escrow addresses elsewhere in this codebase) —
  balances are transparently visible on-chain (explorer/balance
  lookups) but have no real disbursement mechanism yet. Only Foundation
  Treasury is immediately spendable, since it reuses the existing,
  already-built Treasury feature's real operator-controlled address
  rather than getting a second synthetic one. A governance-controlled
  withdrawal mechanism for the other 4 is a natural follow-up, not
  attempted here.
- **`TxAdminTransfer` (`api/admin_transfer.go`) is a deliberate, narrow
  emergency-recovery capability, not a general admin override.** Added
  specifically for this deploy: two production validators (node-001,
  node-002) were seeded by an early bootstrap process with no
  recoverable private key, so once the CCT-stake eligibility fix above
  went live, neither could sign a normal `/stake` transaction to meet
  the new requirement. `TxAdminTransfer` lets the operator move CCT an
  address *already holds* to a new, key-controlled address - it can
  never create new supply (unlike `TxTokenMint`, which already could),
  only relocate existing balance, and only when triggered by the same
  `ADMIN_TOKEN` already gating the mint faucet. Intended purely as a
  one-time migration tool for this specific situation, not a
  general-purpose fund-movement endpoint - each use is visible on-chain
  as an ordinary transaction with `tx.From` empty (node-authored, per
  `verifyTxAuth`'s `TxAdminTransfer` case).

## Economic / Game-Theoretic

- **Several demo-scale timing constants stand in for real values.**
  `UnbondingPeriodSeconds` (60s, vs. a real deployment's likely days),
  `DormancyPeriodSeconds` (300s, vs. an intended ~50-year real dormancy
  period), and `MaxTestStakePerAccount` (explicitly "a shared-pilot fairness
  cap, not an economic security mechanism") all need real-world values set
  before mainnet — they exist at demo scale purely so their lifecycles could
  be exercised quickly during development.
- **Default reputation penalty is now wired up and shortfall-proportional;
  late-but-repaid still is not.** `core/credit_engine.go`'s
  `shortfallReputationDelta` + `DefaultLoan(loan, shortfall, owed)`, driven by
  `api/apply.go`'s `processLoanDefaults` (due-date sweep) and
  `processCollateralLiquidations` (proactive price-crash sweep, OETH
  collateral only) — a real default now scales the borrower's penalty (and
  the lender's mirror-image gain) to how much of the debt liquidation
  actually failed to recover, floored/capped between 1 and the historical
  flat `-30`, with zero penalty if collateral fully covered the shortfall.
  The separate `+3` (repaid-but-late, distinct from default) path still has
  no lateness-detection wired up — `RepayLoan` always treats a repayment as
  on-time regardless of `DueAt`. Confirmed against current `/reputation-math`
  API output: default is now `Wired: true` (previously `false`, was stale
  through the Phase B session); late-but-paid remains `Wired: false`.
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
- **Lending marketplace redesign: Borrowing Power Score weights and the
  client-side Risk Rating are judgment calls, not a locked economic
  model.** (`api/borrowing_power.go`, `website/js/lending-shared.js`)
  The composite score (reputation 40% / repayment history 25% / staked
  CCT 15% / collateral 10% / governance participation 10%) and its
  normalization caps (`stakedCCTNormalizationCap=10000`,
  `governanceParticipationCap=5`) are a reasonable starting formula
  documented in code as exactly that — not derived from a specified
  economic model, and worth revisiting with real usage data before
  the weights are treated as authoritative. The sixth named factor,
  validator endorsements, has no real mechanism anywhere in this
  codebase (`core.Validator.CommunityTrust` is scaffolded, always 0,
  never written) and is reported at an explicit weight of 0 with a
  "not yet active" status — never a fabricated number. The per-loan
  Risk Rating (Very Low–Very High) shown on the marketplace pages is
  a pure frontend display convenience computed from already-fetched
  real data (collateral ratio, reputation, loan size, duration) — it
  is not a new backend source of truth and is not used anywhere in
  the actual lending/liquidation logic.
- **`GET /lender/stats` (`core.LenderStats`) is a known-orphaned
  endpoint from a superseded funding path — do not build new features
  on it.** Confirmed by reading both funding paths in full: the only
  code that ever writes to `lenderStats` is the legacy direct
  `/loans/fund` → `applyFundLoan` path (`api/apply.go`), which no
  current frontend calls. The actual funding path every real lender
  uses today — `applyLoanOfferDraw` (negotiation-confirm) and the new
  `applyLoanRequestFulfill` (see below) — both inline their own
  reputation bookkeeping and never touch `lenderStats` at all, so this
  endpoint returns all-zero/stale data for every real lender. The new
  Lender Dashboard (`website/lend.html`) is deliberately built on a
  new `GET /lender/portfolio` endpoint instead, which computes every
  figure live from `loanStore.Loans` on each read rather than relying
  on this stale running counter. `core.LenderStats.InterestEarned`
  specifically was also found to be mislabeled even on the path that
  does write it — it sums the loan's rate *percentage* at fund time,
  not a currency amount, and fires regardless of whether the loan is
  ever actually repaid; `/lender/portfolio`'s `interestEarned` field
  computes the real collected-currency figure instead
  (`repayAmountFor(loan) - loan.Amount`, summed only for loans that
  reached `REPAID`). Not fixed in place — `lenderStats`/
  `applyFundLoan` are left exactly as they were, matching this
  project's "don't delete, disclose" convention for superseded code.
- **Borrower-initiated loan requests (`core.LoanRequest`) allow real
  over-collateralization; the older self-funded/offer-draw borrow
  paths still don't.** A deliberate, disclosed enhancement, not an
  inconsistency to be "fixed" — `applyBorrow`/`applyLoanOfferDraw`
  both still reserve collateral exactly equal to the loan amount
  (1:1, no choice), while `applyLoanRequestCreate` lets the borrower
  choose any `CollateralAmount >= Amount` in their chosen symbol.
  Verified end-to-end in a live local browser session (two real
  registered wallets, real signed transactions, real fund movement):
  a request for 20 YEN-e against 30 CCT collateral was created,
  discovered by a second wallet, negotiated at an 8% rate (within the
  request's 10% max), confirmed, and settled — the borrower received
  exactly 20 YEN-e, the lender's balance dropped by exactly 20, the
  request flipped to `FULFILLED`, and the resulting `Loan` carried the
  correct negotiated rate and the borrower's original collateral.

## Reputation / Mining

- **Continuous Reputation Credit (REPT) mining is an O(all wallets)
  sweep, gated to a coarse epoch, not a scalable indexed design.**
  `processReputationMining` (`api/reputation_mining.go`) iterates every
  wallet in `walletStore.Wallets` once every `miningEpochBlocks`
  (10,800 blocks, a judgment call approximating ~1 day at the existing
  ~8s consensus tick) to compute each one's Mining Power score and mint
  REPT proportionally. This is bounded to a small fraction of blocks
  rather than running every block, but the per-epoch cost itself still
  scales linearly with total wallet count with no paging/index
  structure — acceptable at the current scale, a real limitation before
  a much larger wallet base. `baseEpochMiningEmission=20` (REPT/epoch at
  MiningPower==100) is also a judgment call sized only to make
  continuous mining meaningfully outpace the reduced one-time tier
  rewards (see below), not derived from any specified economic model.
- **Mining Power's factor weights and two normalization caps are
  judgment calls, not a locked economic model.** `api/mining_power.go`'s
  `miningPowerScore` (Trust Score 30% / Repayment History 20% /
  Staking 15% / Governance 10% / Validator Participation 10% /
  Successful Lending 10% / Account Age 5%) reuses three factors
  verbatim from `api/borrowing_power.go`'s existing Borrowing Power
  Score (via shared helpers) but is otherwise a new, independently
  weighted formula — worth revisiting with real usage data. The eighth
  named factor from the Reputation Credit design, Network
  Contributions, has no real backing data anywhere in this codebase and
  is reported at an explicit weight of 0 with a "not yet active"
  status — never a fabricated number, same convention Borrowing Power
  already uses for Validator Endorsements.
- **Warning/Margin Call dwell times in the Default Protection sequence
  are judgment calls; only Grace Period and Final Redemption are
  spec-locked.** `core/loan_risk.go`'s `LoanRiskWarningDwellSeconds`
  (24h) and `LoanRiskMarginCallDwellSeconds` (48h) were not specified
  by the product design and may need tuning; `LoanRiskGracePeriodSeconds`
  (72h) and `LoanRiskFinalRedemptionWindowSeconds` (24h) are explicit,
  locked values from that design and should not be casually changed.
- **The one-time REPT tier-crossing reward was reduced ~10x
  (`core/reputation_reward.go`: Silver 50→5, Gold 200→20, Diamond
  750→75, Genesis 500→50), a judgment call on the exact scale-down
  factor**, made because continuous mining (above) is now intended as
  the dominant, ongoing REPT source — the tier reward is a secondary
  bonus, not the primary earning path, as it was before this change.
  Relative ordering (including the pre-existing, intentional quirk that
  Genesis pays less than Diamond) is preserved unchanged.
- **New notification types for the Default Protection sequence
  (`LOAN_RISK_WARNING`, `LOAN_MARGIN_CALL`, `LOAN_GRACE_PERIOD_STARTED`,
  `LOAN_LIQUIDATED`, `LOAN_FINAL_REDEMPTION_NOTICE`, `LOAN_REDEEMED`)
  are in-app only.** No email/SMS/push delivery infrastructure exists
  anywhere in this codebase beyond the one already-disclosed email
  placeholder (`api/recovery.go`, entry above) — consistent with that
  existing gap, not a new one, and the product spec itself named SMS as
  explicitly optional.

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
- **Real SMTP is wired in code but not configured in production.**
  `api/email.go` implements a real SMTP sender (STARTTLS + AUTH PLAIN,
  stdlib only), and both `api/recovery.go` (recovery-code emails) and
  `api/hub_rate.go` (hub-attention notifications) already call it when
  `smtpConfigured()` is true. Neither node currently has `SMTP_HOST`/
  `SMTP_USERNAME`/`SMTP_PASSWORD`/`SMTP_FROM` set, so both fall back to
  their testnet-placeholder behavior live (recovery code returned directly
  in the HTTP response with an explicit "TESTNET MODE" label; hub
  notifications silently skipped) — see `ops/EMAIL_SETUP.md` for the setup
  steps once an operator is ready to supply real SMTP credentials.
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
- **QR code encoder had 3 real placement bugs, now fixed, plus a new
  hand-rolled decoder for the in-app scanner.** The above entry
  originally flagged that the encoder was checked only structurally,
  never against a real scan - that risk materialized: the QR scanner
  ("does not yet work") turned out to have two independent problems.
  (1) `website/js/send.js`/`send-eth.js` gated all scanning on the
  browser-native `BarcodeDetector` API, which only ships on Android
  Chrome/ChromeOS - absent on desktop Chrome/Edge/Firefox/Safari, so
  the scanner silently failed for nearly everyone regardless of the
  encoder. (2) `website/js/qrcode.js` itself had 3 genuine bugs in its
  zigzag data-placement logic, meaning the QR codes it rendered were
  actually corrupted and would fail on *any* correct scanner, not just
  this app's own: (a) format-info bits were written into the matrix
  *after* the data-placement loop instead of before, so those cells
  looked "available" during placement and got clobbered; (b) it drew
  alignment patterns at all 4 combinations of the version-5 coordinate
  list `[6, 30]` instead of the single spec-correct position `(30,
  30)`, so 2 of the 3 "alignment patterns" silently overwrote parts of
  the top-right/bottom-left finder patterns; (c) the column-pair walk
  substituted column 5 for the reserved timing column (6) on only the
  one iteration where it hit exactly 6, without shifting subsequent
  iterations, causing column 4 to be visited (and overwritten) twice
  while column 0 was never visited at all. All 3 are fixed in
  `qrcode.js` directly (see its inline comments for the mechanism).
  Fix for (1): a new hand-rolled `website/js/qr-decode.js` reads QR
  codes without needing `BarcodeDetector` - deliberately scoped to
  exactly what `qrcode.js` produces (fixed Version 5 / EC level L /
  mask 0 / byte mode), not a general-purpose QR decoder. It has no
  Reed-Solomon error correction; instead a scanned code's data is
  verified by locally recomputing its expected EC codewords (via the
  same `QR.computeECCodewords` the encoder itself uses) and requiring
  an exact match - a clean capture decodes, a bad/blurry one is
  rejected with "try again" rather than silently returning corrupted
  data. Verified via round-trip tests (encode then decode, byte-for-
  byte match) across multiple addresses plus synthetic rotation/blur/
  downscale variants, and via a real end-to-end browser test driving
  the actual file-upload scan path. Known residual limitation, worth
  flagging to an auditor: no live-camera-in-the-loop test was possible
  in this environment (no physical camera) - only the file-upload
  fallback path was exercised against a real `<input type="file">`
  element; the live-video path shares the same `decodeImageSource`
  function so this is low risk, but hasn't been confirmed against an
  actual phone camera.

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

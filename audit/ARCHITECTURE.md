# CreditChain — Architecture Overview

This document is part of the pre-audit package. It describes the system as it
actually exists in code today, not as originally envisioned — where the two
differ, this document follows the code. See `KNOWN_GAPS.md` for a
consolidated list of every deliberate simplification/limitation, and
`THREAT_MODEL.md` for the actors/attack-surface analysis this architecture
implies.

## 1. What this is

CreditChain is a single custom Layer-1 blockchain (not a fork of an existing
chain, not an L2) implemented from scratch in Go, plus a static-HTML/JS
website frontend. It is currently **testnet-only**: no token has been
represented to users as holding real-world value, and no mainnet launch has
occurred as of this writing.

The system covers: account/wallet management, a native token (CCT) plus a
governance-reputation token (REPT) plus two "imported" tokens (ETHTEST,
USDCTEST) used to simulate bridged assets, staking with an unbonding period,
peer-to-peer block production and gossip, a permissioned validator set,
collateralized lending, a peer-to-peer trading marketplace gated to
moderator-verified "Trading Hubs," a reputation/credit-scoring system, and a
non-custodial Bitcoin balance viewer.

## 2. Process topology

There is exactly one Go binary, `creditchaind` (built from `main.go` +
`api/` + `core/`), which is both the blockchain node and the HTTP API server
in the same process — there is no separate "full node" vs "API server" split.
A second, much smaller binary, `staticserve` (`cmd/staticserve/main.go`),
serves the `website/` static files; it has no application logic of its own.

Production currently runs two `creditchaind` instances (node1, node2) on
separate hosts, each paired with its own `staticserve` instance, gossiping
over HTTP to each other (see §5). This is a small, fixed, operator-known
validator set — not an open/permissionless network (see §5 and
`KNOWN_GAPS.md`).

## 3. Code layout

- **`core/`** (102 files) — pure domain logic. No HTTP handling, no
  package-level mutable singletons representing "the" application state
  (stores are instantiated, not global, at this layer) — every `*Store` type
  here (e.g. `WalletStore`, `TokenStore`, `HubRateStore`) is a self-contained,
  mutex-guarded in-memory map with `Snapshot()`/`Restore()` for persistence,
  constructed independently by whatever owns it.
- **`api/`** (90 files) — HTTP handlers, route registration
  (`api/server.go`), and the actual runtime state: this package instantiates
  package-level singleton stores (`walletStore`, `tokenStore`, `hubRateStore`,
  etc. — see individual files for the `var xStore = core.NewXStore()`
  declarations) and wires HTTP requests to them. This is also where
  consensus orchestration lives (`api/consensus_loop.go`), where
  transactions actually get applied to state (`api/apply.go`, by far the
  largest and most security-critical file), and where peer-to-peer gossip is
  handled (`api/peer.go`).
- **`website/`** — static HTML + vanilla JS (no build step, no framework).
  `website/js/api.js` is the shared HTTP client every page's own JS file
  uses; `apiPostSigned()` is what signs a mutating request client-side
  (see §6).
- **`cmd/`** — two small standalone binaries: `chaindiff` (an operator
  debugging tool for comparing two nodes' chains, used repeatedly during
  fork-recovery incidents — see `KNOWN_GAPS.md`) and `staticserve` (the
  website file server, §2).
- **`ops/`** — deployment/operations material: the encrypted-backup script
  and its systemd units, node-operator onboarding docs.
- **`audit/`** (this directory) — the pre-audit documentation package.

## 4. Identity and the transaction-authenticity model

There is no server-side account/password system for on-chain identity.
`core.GenerateAddress(publicKey) = SHA256(publicKey)[:20]` (hex-encoded) — an
address is entirely self-certifying: anyone can verify a claimed address
really corresponds to a given public key without a lookup, and a wallet is
just an Ed25519 keypair generated client-side (`website/js/wallet.js`),
never transmitted to or stored by the server.

Every mutating HTTP request is wrapped in a `core.SignedEnvelope`
(`core/request_auth.go`): `{address, publicKey, timestamp, payload,
signature}`, where `signature` covers `address|publicKey|timestamp|payload`
(Ed25519). `api/auth_middleware.go`'s `requireSignedRequest` verifies this on
the way in: recomputes the address from the public key, checks the
timestamp is within a ±60s freshness window, verifies the signature, and
checks a short-lived replay cache. This is what stands between "anyone can
claim to spend anyone's money" and not.

That alone is not sufficient, because the actual state change happens later,
asynchronously, when a **block producer** (not necessarily the original
submitter) includes the transaction in a block. `core.Transaction` therefore
separately carries its own `Auth*` fields (`AuthAddress`, `AuthPublicKey`,
`AuthTimestamp`, `AuthPayload`, `AuthSignature`) — a full copy of the
original signed envelope — so that **any node applying a block can
independently re-verify, from the transaction alone, that a real signature
authorized exactly this economic action**, without trusting the block
producer's honesty. `core.VerifyTransactionAuth` does the cryptographic
check; `api/apply.go`'s `verifyTxAuth` does a second, transaction-type-
specific check that the transaction's actual effect (amount, recipient,
etc.) matches what was cryptographically signed — this is the layer that
catches a malicious block producer altering a transaction's effect *after*
a valid signature was obtained on a different payload. Every `ApplyTransaction`
call path goes through both layers; `api/apply_test.go` and
`api/hub_operator_test.go` specifically test the "signature valid but claimed
effect doesn't match signed payload" attack.

## 5. Consensus and networking

**Model**: permissioned proof-of-stake-flavored round-robin, not proof-of-
work, not BFT. `core/validator_store.go` tracks the registered validator
set (registration is itself a signed transaction — see §4 — so it can't be
server-asserted); `api/consensus_loop.go` runs each node's local production
loop, deciding whose turn it is to produce and building/broadcasting blocks
on that schedule.

**Peer gossip** (`api/peer.go`, `core/peer.go`): nodes exchange transactions
and blocks over plain HTTP endpoints (`/peer/tx`, `/peer/block`), gated by a
single shared secret (`NETWORK_SECRET`, checked with
`subtle.ConstantTimeCompare` against an `X-Network-Secret` header) — this is
a **permissioned network**: joining requires an operator to hand you the
secret and add your node's URL to every existing node's `PEERS` config by
hand (see `ops/NODE_OPERATOR.md`). There is no self-serve join flow, no
reputation-weighted peer scoring, and holding the secret carries no
economic/reputation cost — so this is still not Sybil-resistant in the
sense of "expensive to mint many identities." As of 2026-08-10, the
handshake itself (`HandlePeerHandshake`) requires a proof-of-possession
signature over the claimed `NodeID`/`PublicKey`/`Address`, and block
adoption (`verifyBlockProducer`) verifies against `ValidatorStore`'s own
on-chain-registered `AuthorizedNodePublicKey` rather than `peerStore`'s
self-asserted one — closing the specific escalation where a
`NETWORK_SECRET` holder could impersonate a known validator's `NodeID` and
have a self-forged block wrongly verify (see `KNOWN_GAPS.md`).

**Fork resolution** (`core/replace_chain.go`): `ValidateChain` checks
internal hash-chain consistency (each block's `Hash` really is
`ComputeBlockHash` of its own contents, indices are sequential, links are
unbroken) but does **not** independently re-verify that each block's
producer was actually authorized to produce it — that authorization check
(`AuthorizedNodeID` binding, see below) happens separately in `peer.go`'s
`adoptBlock`/`verifyBlockProducer` before a block is ever adopted.
`CommonPrefixLen` finds where two candidate chains diverge. This
infrastructure has been exercised for real during this project's testnet
phase: two genuine node1/node2 forks occurred and were manually resolved
(see `KNOWN_GAPS.md` and `HARDENING_HISTORY.md` for what was hardened as a
result).

**Validator-to-node binding**: a validator's registration transaction embeds
which node the owner is delegating block production to
(`AuthorizedNodeID`/`AuthorizedNodePublicKey` on `core.ValidatorStatus`) —
`consensus_loop.go`'s local-production gate and `peer.go`'s block-adoption
check both require this, not just "is this a known validator address," which
closes an impersonation gap where an unrelated node could produce (and
collect relay reward for) blocks on behalf of a validator it doesn't
actually operate.

**Slashing**: replicated via a signed `TxSlashReport` transaction
(self-signed by the reporting node's own identity keypair — see
`core/node_identity.go`) rather than each node mutating
reputation/stake locally the instant it observes a fault. Double-sign
reports are independently re-verified from embedded evidence (both
conflicting blocks, checked for a genuinely matching index/validator/hash-
consistency/valid-signature) before the penalty applies — a report alone
isn't trusted. Missed-block reports have no equivalent cryptographic proof
(absence can't be proven) and remain single-reporter-trusted; see
`KNOWN_GAPS.md`.

## 6. Money-movement path (the core security boundary)

For any economic action (transfer, stake, borrow, P2P trade, etc.):

1. Client builds the action's payload, wraps it in a signed envelope
   (`website/js/api.js`'s `apiPostSigned`), POSTs it.
2. Handler (`api/*.go`) calls `requireSignedRequest`, does *fast-fail*
   validation (cheap checks: is the amount positive, does the submitter's
   currently-known balance look sufficient) purely to reject obviously-bad
   requests quickly — this check is **not** the real authorization; it's
   explicitly documented as re-checked at confirmation time since submission-
   time state can be stale.
3. Handler calls `recordTx(...)`, which builds a `core.Transaction` carrying
   the verified `Auth*` fields and appends it to the local pending pool.
4. The transaction gossips to peers (`/peer/tx`).
5. Whichever node's turn it is includes the transaction in a block it
   produces; every node that receives that block (including the producer
   itself) runs `ApplyBlock` → `ApplyTransaction` → the type-specific
   `applyX` function in `api/apply.go`, which re-verifies auth (§4), re-
   checks the real live balance/state (not the submission-time snapshot),
   and only then mutates `walletStore`/`tokenStore`/etc.
6. Deduplication happens at two levels: by transaction ID
   (`appliedTx.seen`) and by block hash (`appliedBlock.seen`), so a
   replayed/re-gossiped tx or block is a no-op the second time — tested in
   `api/apply_test.go`.

This same "escrow now, settle/release later" pattern is used for every
counterparty-risk scenario in the app (P2P trade escrow, collateralized
loans, hub-rate trades) rather than a single all-purpose "spend" primitive —
see `core/p2p_offer.go`'s settlement state machine
(`AWAITING_PAYMENT → SELLER_CONFIRMED → CONFIRMED/DISPUTED →
RESOLVED_RELEASE/RESOLVED_REFUND`) as the canonical example, extensively
tested in `core/p2p_offer_test.go`.

## 7. Token model

- **CCT** — the native token. Balances live directly at a wallet's own
  address.
- **REPT** — a reputation/governance token, same storage model as CCT.
- **ETHTEST / USDCTEST** — "imported tokens" simulating bridged assets.
  Critically, **these do not live at the wallet's own CCT address** — each
  is derived via `core.DeriveTokenAddress(publicKey, tag)` into its own
  sub-address, specifically so that a P2P/loan/pool action against one of
  these tokens can never be authorized merely by proving control of the CCT
  address; the caller's real public key must be presented and independently
  re-derive the correct sub-address (`api/subaddress.go`'s
  `resolveBalanceAddress`) every time. This was a deliberate hardening
  response to an earlier design where a single address controlled every
  token's balance uniformly (see `HARDENING_HISTORY.md`).
- Custom developer-registered tokens (`core/token_registry.go`) follow the
  CCT model (own-address balances), not the sub-address model.

## 8. CCT order book (`core/order_book.go`, `api/order_book.go`)

CCT's price against ETHTEST/USDCTEST comes from a real limit-order-book
matching engine, not the constant-product AMM described in §7's sibling
pool code — deliberately narrower than a general-purpose exchange (see
`KNOWN_GAPS.md` for exactly what's out of scope: no trading fee, no order
expiry, no wash-trading protection).

An `Order` is a resting (or once-resting, never deleted) limit order:
`Side` (`BUY` pays CCT for the base token, `SELL` gives up the base token
for CCT), `Price` (CCT per whole unit, scaled by the same `priceScale`
(1e6) `core/price_history.go`'s pool-reserve price already uses — one
consistent unit across the AMM chart and the order book), `Amount`/
`Remaining`, and `Status` (`OPEN`/`FILLED`/`CANCELLED`).
`OrderBookStore.Match(taker)` is pure bookkeeping with **zero token-store
access** — it sorts opposite-side resting orders by price-time priority
(best price first, ties broken by `CreatedAt` then `ID`, never by Go map
iteration order, which is randomized and would break cross-node
determinism) and fills at each resting order's own price (maker sets the
price — a marketable order can receive price improvement). Self-trading
(same `Owner`) is skipped. This separation — matching logic in `core/`
with no balance access, fund movement in `api/apply.go`'s
`applyOrderPlace`/`applyOrderCancel` — mirrors `core/p2p_offer.go`'s
existing split and keeps the consensus-critical matching algorithm
unit-testable in complete isolation (`core/order_book_test.go`).

Funds move through a per-order synthetic escrow address
(`"ORDERBOOK:" + orderID`, reusing the placing transaction's own ID) — the
same per-instance escrow pattern `p2pOfferEscrowAddress` already uses for
P2P offers, deliberately distinct from the AMM's shared per-symbol
`poolAddress`, since many simultaneous open orders must never commingle.
A `TradeRecord` produced by `Match` carries both `BuyOrderID`/
`SellOrderID` explicitly (not just the two `Owner` addresses), since the
same owner can have multiple simultaneous orders on one symbol —
`applyOrderPlace` looks each side back up (from the taker itself, or from
`OrderBookStore` for a resting order) to find its escrow and its
`PublicKey` for sub-address resolution (§7).

Trade prices feed the existing price-history/chart pipeline via a new
sibling method, `PriceHistoryStore.RecordPrice(symbol, price, timestamp)`,
alongside the pre-existing pool-reserve-coupled `Record` — so
`GET /pool/price-history` and `price-chart.js`'s rendering needed zero
changes; only the data source underneath switched from AMM reserve ratio
to real trade prices for these two symbols.

The AMM pools for ETHTEST/USDCTEST are **not deleted** — `cctEthTestPool`/
`cctUsdcTestPool` (§7) remain fully live internally, still used by
`creditMaturedETHTestViaSwap`'s unstake-redemption credit path. Only the
user-facing swap/add-liquidity UI stops offering these two symbols
(existing liquidity providers can still remove what they already
deposited) — same "hide, don't delete" precedent used elsewhere in this
codebase.

## 9. Trading Hubs / marketplace

A "Trading Hub" is a wallet address that applied via `core/company.go`'s
`CompanyApplication` flow and was approved by a registered moderator — see
`KNOWN_GAPS.md` for exactly what "verified" does and doesn't mean here. Once
verified, a hub can post standing OTC rates (`core.HubRate`) against its own
live balance (no pre-locked amount — the hub's current balance *is* the
tradeable range, checked fresh at every trade), and can delegate to
**operators** (`core.HubWorker`): as of the most recent change, each active
operator runs their own fully independent listing/inventory
(`HubRate.OperatorAddress`) under the parent hub's verified badge, rather
than sharing the hub's own balance — this is what makes "one verified hub"
support multiple genuinely separate marketplaces.

Trades settle either **ONCHAIN** (both legs move atomically in one
transaction, no dispute possible since both sides are on-chain-verifiable
balances) or **EXTERNAL** (one leg is a real off-app payment CreditChain
cannot verify — see `core.P2POffer`'s `SettlementStatus` state machine and
`core/moderator.go`'s dispute-resolution path).

## 10. DeFi corner (Bitcoin + custodial ETH)

`website/defi.html` / `website/js/defi.js`'s BTC section is a read-only
Bitcoin balance/transaction-history viewer against the public Blockstream
API — it never asks for, generates, stores, or transmits a BTC private key,
and never constructs or broadcasts a Bitcoin transaction.

The same page's ETH section is categorically different: a **custodial**
corner where CreditChain generates and holds a real Ethereum signing key on
the user's behalf, scoped to **Sepolia testnet only** (never mainnet — see
below). `core/eth_custodial.go` defines the account model;
`api/eth_signer.go` is the actual key-custody boundary — an `EthSigner`
interface with a real AWS KMS-backed implementation (the private key
material never leaves KMS; signing is done via `kms:Sign` against a
pre-computed digest, with the recovery id KMS doesn't return brute-forced
against the key's own known public key) and a clearly-labeled local-dev-only
fallback for testing without AWS provisioned. `api/eth_rpc.go` handles the
actual Sepolia RPC connection (via go-ethereum's `ethclient`) and refuses to
enable at all if the configured RPC URL's chain ID isn't Sepolia's —
specifically to make "an operator accidentally points this at mainnet" loud
and immediate rather than a silent, catastrophic mistake.

Unlike every other economic action in this app (§6), custodial account
creation and withdrawal execute synchronously in the HTTP handler on
whichever node receives the request, not through the gossip/block-
confirmation pipeline — broadcasting a real (if testnet) Ethereum
transaction is an external, irreversible side effect that must happen
exactly once, and routing it through consensus would risk two nodes racing
to broadcast the same transaction. The disclosed consequence: a custodial
account is node-local, not replicated — see `KNOWN_GAPS.md` and
`ops/ETH_CUSTODIAL_SETUP.md`.

Real Ethereum mainnet custody remains explicitly out of scope and would need
its own dedicated security review plus a real money-transmission licensing
review before any change points this at real value — see `KNOWN_GAPS.md`.

## 11. Persistence

Current mechanism (`api/persistence.go`): every store's `Snapshot()` is
serialized into one `state.json`, and the full block list into
`blockchain.json`, rewritten wholesale on every save — not an append-only
log, not a real database, no transactional guarantees across stores. See
`KNOWN_GAPS.md` and the production-readiness plan's Phase 3 (Postgres
migration, not yet started) for the planned remediation. Off-server
encrypted backups exist and are confirmed working in production (§ Phase 0)
independent of that migration.

## 12. What auditors should look at first

Given the above, the highest-value places to focus review time are:
1. `core/request_auth.go` + `api/auth_middleware.go` + `api/apply.go`'s
   `verifyTxAuth` (the entire transaction-authenticity boundary, §4/§6).
2. `core/replace_chain.go` + `api/peer.go`'s block-adoption/producer-
   authorization checks (§5) — this is the code that has already caused two
   real production incidents (forks) this testnet phase, and node2's
   recurring instability (see `KNOWN_GAPS.md`) means this code path is
   still actively load-bearing, not just historically relevant.
3. `api/subaddress.go` + every call site of `resolveBalanceAddress` (§7) —
   the sub-address model is easy to get subtly wrong at a new call site.
4. `core/order_book.go`'s `Match` + its determinism guarantees (§8) — the
   newest consensus-critical code in the app, and the one place a subtle
   non-determinism bug (e.g. an accidental map-iteration dependency) would
   be hardest to notice locally but would directly cause a chain fork in
   production, the exact failure mode already seen twice for other code
   paths.
5. `core/p2p_offer.go` + the hub-rate/multi-operator settlement paths in
   `api/apply.go` (§9) — the largest surface of "move real value between
   two untrusted parties" logic in the app.

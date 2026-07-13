# CreditChain — Threat Model

This is a working threat model for the system as described in
`ARCHITECTURE.md`, written to give an auditor a starting map of actors,
assets, and attack surfaces rather than to claim every risk has been
fully mitigated — cross-reference `KNOWN_GAPS.md` for what's explicitly
still open.

## 1. Actors

| Actor | Capability | Trust level |
|---|---|---|
| Regular user (wallet holder) | Signs and submits their own transactions | Untrusted — assumed potentially adversarial toward other users |
| Trading Hub owner | Verified by a moderator; posts standing OTC rates against own balance; can delegate operators | Semi-trusted (moderator-vouched, not cryptographically verified identity) |
| Hub operator | Delegated by a hub owner; runs an independent listing under the hub's badge | Same trust level as any regular user w.r.t. their own funds; trusted by the *hub* to represent it, not trusted by CreditChain beyond that |
| Moderator | Registered role; approves/rejects hub applications, resolves P2P disputes | Trusted for attestation/dispute judgment calls; **not** trusted with any direct fund-moving authority — every moderator action either gates a status flag or triggers an already-escrowed release, never a fresh unilateral transfer |
| Validator / node operator | Runs a `creditchaind` instance; produces blocks on an authorized schedule | Trusted within a small, operator-known, permissioned set — not an open/adversarial-by-default validator set (see `ARCHITECTURE.md` §5) |
| Network attacker (outsider) | Can send arbitrary HTTP requests to any public endpoint; can attempt to relay fake gossip if they obtain/guess `NETWORK_SECRET` | Fully untrusted |

## 2. Assets at risk

1. **User token balances** (CCT, REPT, ETHTEST, USDCTEST, custom tokens) —
   the primary asset; unauthorized movement is the single most severe
   failure mode.
2. **Private keys** — never transmitted to or stored by the server for any
   wallet (client-side Ed25519 keypairs); the BTC DeFi-corner viewer is
   explicitly non-custodial for the same reason. **The ETH custodial
   corner is the one exception**: CreditChain genuinely holds a real
   Ethereum signing key per custodial account (via AWS KMS in production —
   key material never leaves KMS — or a clearly-labeled local dev-only
   fallback), scoped to Sepolia testnet only. See §3.13 below and
   `KNOWN_GAPS.md`.
3. **Reputation/credit score** — not directly redeemable for funds, but
   gates real economic outcomes (loan terms, validator eligibility
   perception) — manipulating it is a real (if lower-severity) attack goal.
4. **Validator block-production rights** and the **rewards** attached to
   them.
5. **The chain's own consistency** — two nodes disagreeing about history
   (a fork) is itself an asset-at-risk, independent of any single
   transaction, since it undermines the "one true ledger" property the
   whole system depends on.
6. **Hub-operator inventory isolation** — since the multi-operator
   marketplace change, one operator's funds must never be reachable via an
   action nominally targeting a different operator's (or the hub's own)
   listing.

## 3. Attack surfaces and current mitigation

### 3.1 Forging a transaction as someone else
**Mitigation**: `core.SignedEnvelope` + `core.VerifyTransactionAuth` +
`api/apply.go`'s `verifyTxAuth` (see `ARCHITECTURE.md` §4,
`HARDENING_HISTORY.md` §1–2). Covered by `core/request_auth_test.go` and
`api/apply_test.go`.
**Residual risk**: the freshness window is ±60s — a captured-and-replayed
envelope within that window before the replay cache catches it is a
theoretical race; the replay cache itself is in-memory and per-node (not
cross-node), so a malicious block producer could in principle accept a
replayed envelope another node already rejected. Not observed as
exploited; worth auditor attention.

### 3.2 A block producer altering a transaction's effect post-signature
**Mitigation**: `Auth*` fields on `core.Transaction` let *every* node
independently re-verify, not just the one that first received the request
(`HARDENING_HISTORY.md` §2). Tested directly (amount-mismatch,
recipient-mismatch).
**Residual risk**: none identified beyond what's covered by tests, but this
is exactly the kind of invariant worth an auditor trying to break directly
rather than trusting the test suite.

### 3.3 Impersonating a validator / forging block-production rights
**Mitigation**: `AuthorizedNodeID` binding (`HARDENING_HISTORY.md` §3),
checked at both local production and peer block-adoption time.
**Residual risk**: the binding is only as strong as `NETWORK_SECRET`
(§3.6) and the underlying registration signature; a validator that loses
control of their own key has no separate revocation mechanism beyond
re-registering (which itself requires the same, now-compromised key).

### 3.4 A malicious/compromised node falsely reporting a fault
**Mitigation**: double-sign reports are independently re-verified from
embedded evidence before any penalty applies (`HARDENING_HISTORY.md` §4).
**Residual risk, explicitly disclosed**: missed-block reports have no
equivalent proof and remain trusted from a single reporter — a compromised
or buggy node could falsely slash an honest validator's reputation this
way. See `KNOWN_GAPS.md`.

### 3.5 Cross-token privilege escalation (CCT authority reaching
ETHTEST/USDCTEST)
**Mitigation**: per-token sub-addressing requiring the caller's real public
key (`HARDENING_HISTORY.md` §5).
**Residual risk**: the flip side of this same design — the server's
inability to reach these sub-addresses without a stored public key — is
itself a disclosed availability/recoverability gap (dormancy sweep,
wallet recovery, explorer lookups), not a fund-safety gap. See
`KNOWN_GAPS.md`.

### 3.6 Gossip/peer-network attacks (fake blocks, fake transactions,
Sybil nodes)
**Mitigation**: `NETWORK_SECRET` shared-secret gate on all `/peer/*`
endpoints, `AuthorizedNodeID` binding on block adoption (§3.3),
`ValidateChain`'s internal hash-consistency check.
**Residual risk, explicitly disclosed**: this is a coarse, single-secret,
permissioned-network model — no per-peer identity, no Sybil resistance, no
self-serve join (`KNOWN_GAPS.md`). Appropriate for a small, operator-known
validator set; would need real work before any move toward a
permissionless network.

### 3.7 Fork / chain-consistency attacks
**Mitigation**: `ValidateChain` + `CommonPrefixLen`
(`core/replace_chain_test.go`, 12 tests) plus the operational hardening
from two real incidents (`HARDENING_HISTORY.md` §8).
**Residual risk, explicitly disclosed and real-incident-tested**: no state
rollback on a reorg past an already-applied block — the system currently
handles "extend what I have" correctly but not "adopt a competing history
after I've already committed past the fork point." This is the single
highest-value area for an auditor to focus on, given it's already caused
two real production incidents. See `KNOWN_GAPS.md` and
`ARCHITECTURE.md` §12.

### 3.8 Trading Hub / marketplace fund-movement attacks
**Mitigation**: escrow-based settlement for every counterparty-risk
scenario (`core/p2p_offer.go`'s state machine, extensively tested in
`core/p2p_offer_test.go`); hub-gating (only moderator-verified hubs, or
their explicitly-delegated operators, can create EXTERNAL offers);
inventory isolation between a hub and its operators
(`api/hub_operator_test.go`).
**Residual risk**: moderator dispute resolution is human judgment, not
cryptographic verification of an off-app payment — a colluding or
careless moderator could release escrowed funds incorrectly. This is
explicitly disclosed as the accepted trust model for off-app settlement
(`KNOWN_GAPS.md`) — there is no way to make this cryptographically
verifiable without the off-app payment rail itself being on-chain, which
is out of scope by design.

### 3.9 Arbitrary code execution via hub storefronts
**Mitigation**: sandboxed iframe (`sandbox="allow-scripts"` only,
deliberately without `allow-same-origin`), so a hub's custom HTML/CSS/JS
can render its own presentation but cannot reach the parent page's origin,
cookies, or `localStorage` — critically, cannot read a visitor's wallet
keys.
**Residual risk**: this entire feature's safety rests on one specific
iframe-sandbox flag combination never regressing; worth an auditor's
explicit sign-off and a regression test if one doesn't already exist,
given how severe the failure mode would be (wallet-key exfiltration) if
it ever did.

### 3.10 Denial of service
**Mitigation**: per-IP token-bucket rate limiting, `http.MaxBytesReader`
request-body cap, string-length caps on user-supplied text.
**Residual risk**: this is explicitly disclosed as "closes the trivial
local-DoS gap," not a production-grade edge/WAF-level DoS defense — see
the production-readiness plan's Phase 4 (CDN/WAF in front of the public
domain, not yet done).
**Resolved this session**: node1 was found running with only ~50Mi of
available memory and already using swap — no attacker input required to
hit real resource exhaustion at that point, ordinary organic load growth
could have done it. Fixed via a resize to 4GB RAM (see `KNOWN_GAPS.md`
for the full procedure and a hazard worth remembering for any repeat of
it: Plesk-blueprint-origin snapshots need their host-level iptables
firewall opened for port 8080 on every new instance, not just once).
Confirmed ~2.3GB available post-resize. The underlying instance-size
constraint that made this attack surface unusually easy to trip is gone,
though the broader point stands: this is still "closes the trivial
local-DoS gap," not a production-grade edge/WAF-level defense.

### 3.11 Infrastructure / credential-boundary attacks
**Residual risk, currently unresolved (this session)**: the off-server
backup script's AWS calls were observed authenticating via an unexpected
`ASIA`-prefixed temporary credential rather than the configured long-lived
backup-only IAM user — meaning the actual credential/identity boundary in
effect on that production server isn't fully understood yet. Not a
data-loss risk (backups are confirmed working and persisted), but exactly
the kind of "who actually has access, and why" question an infra-focused
review should chase down before sign-off. See `KNOWN_GAPS.md`.

### 3.12 CCT order book (`core/order_book.go`, `api/order_book.go`)
CCT's price against ETHTEST/USDCTEST is now set by a real limit-order
matching engine (bids/asks, price-time priority, price = last completed
trade) rather than an AMM reserve ratio — see `ARCHITECTURE.md` §8.
**Mitigation**: matching is pure, deterministic bookkeeping
(`OrderBookStore.Match`) with no reliance on map-iteration order — every
node applying the same sequence of `TxOrderPlace`/`TxOrderCancel`
transactions reaches byte-identical resting-order state and trade
history, the same replay-determinism property every other consensus-
critical state transition in this codebase already has. Self-trading is
explicitly blocked (an order never matches the same `Owner`'s own resting
order). Funds move through per-order escrow addresses
(`ORDERBOOK:<orderID>`), one per order, never commingled — the same
per-instance escrow pattern already used for P2P offers.
**Residual risk**:
- **No trading fee and no order expiry** (deliberately out of scope for
  this pass, see `KNOWN_GAPS.md`) — an order rests indefinitely until
  filled or cancelled, and matching a trade costs nothing beyond the
  existing flat 1 CCT transfer fee that applies to CCT movement
  generally. A market with no maker/taker cost structure has different
  spam/wash-trading economics than a real exchange; worth modeling before
  mainnet if trading volume becomes meaningful.
- **Wash trading / self-dealing across two colluding wallets is not
  prevented**, only literal self-trading (same address) is blocked. Two
  wallets controlled by the same actor can trade with each other freely,
  same as any real exchange without KYC-linked account clustering — this
  is a known, generally-accepted limitation of pseudonymous order books,
  not specific to this implementation, but worth stating explicitly since
  the resulting trade price feeds `/pool/price-history`'s chart and any
  future price-dependent logic (e.g. the borrow marketplace's
  price-adjusted interest rate, `core/interest.go`) could be manipulated
  by a well-funded wash-trading campaign.
- **Matching happens at block-confirmation time, not submission time** —
  same as every other economic transaction in this codebase, meaning
  whichever validator produces the block that contains two crossing
  orders effectively controls their relative ordering within that block
  (no separate mempool-ordering guarantee beyond whatever order the
  producing node happened to include them in). This is the same
  block-producer trust assumption already disclosed for other
  transaction types (§3.2), not a new attack surface, but worth an
  auditor's attention given it's now gating a real price-discovery
  mechanism rather than just a fund transfer.
- **The AMM pools for ETHTEST/USDCTEST are not deleted, just hidden from
  new user-facing swaps/liquidity additions** — they remain fully live
  internally (`creditMaturedETHTestViaSwap`'s unstake-redemption path
  still reads/writes `cctEthTestPool`/`cctUsdcTestPool` directly). An
  auditor should confirm no code path can be tricked into treating that
  internal AMM price as the "real" market price now that the order book
  is the intended source of truth for those two pairs.

### 3.13 ETH custodial corner (Sepolia testnet only)
**Context**: this is the one place in the codebase where CreditChain
holds a real external-chain signing key on a user's behalf (`api/eth_signer.go`,
`api/eth_rpc.go`, `api/eth_custodial.go`) — everywhere else (BTC viewer,
every native token) is explicitly non-custodial. Scoped to Sepolia only;
`InitEthRPC` calls `client.ChainID(ctx)` at startup and refuses to enable
the corner at all (leaves the RPC client nil) unless the result is
exactly Sepolia's chain ID — a deliberate, loud fail-safe against an
operator accidentally pointing this at mainnet.

**Mitigation, production path**: `kmsEthSigner` never lets a private key
leave AWS KMS — `CreateKey`/`GetPublicKey`/`Sign` only, with `Sign`
operating on a pre-computed digest (`MessageType: DIGEST`), so KMS signs
exactly the Ethereum transaction hash and nothing else. The recovered
public key/address is independently re-derived from KMS's own
`GetPublicKey` response, never trusted from a caller-supplied value.

**Residual risk, explicitly disclosed**:
- **Dev-fallback signer**: `localDevEthSigner` holds real secp256k1
  private keys in an in-memory map (not KMS-backed) when
  `ETH_CUSTODIAL_KMS_ENABLED` isn't set to `true` — clearly logged at
  startup either way (see the `InitEthSigner` log line), but an operator
  who never sets that env var is silently running real (if low-value,
  testnet-only) key custody in a form that doesn't survive a process
  restart and isn't hardware-backed. Acceptable for the current
  developer-testing phase; not acceptable to carry into any real launch
  without a hardware/KMS backend enforced, not just defaulted-to.
- **Synchronous, node-local execution**: unlike every other economic
  action in this app, custodial account creation and withdrawal execute
  synchronously in the HTTP handler on whichever node receives the
  request — deliberately not routed through gossip/consensus, since
  broadcasting a real (even testnet) Ethereum transaction is an
  external, irreversible side effect that must happen exactly once, and
  routing it through consensus would risk two nodes racing to broadcast
  the same transaction. The direct consequence: a custodial account's
  `EthCustodialStore` entry lives in whichever node created it
  (persisted to that node's own `state.json`) and is **not gossiped or
  replicated to the other node** — querying balance/withdrawing from the
  "wrong" node (the one that didn't create the account) will not find
  the account. This is a real availability gap (not a fund-safety one)
  worth an auditor's explicit sign-off before this corner handles
  anything beyond test value.
- **One KMS key per custodial account**: no batching/shared-wallet
  design — acceptable for the current testnet-scale usage, explicitly
  not a cost-efficient or operationally simple pattern to scale past it.
  See `ops/ETH_CUSTODIAL_SETUP.md`'s "known limitations" section for the
  shared-hot-wallet-plus-ledger alternative this deliberately doesn't
  implement yet.

## 4. Explicitly out of scope for this threat model

- **Legal/regulatory risk** (money-transmission licensing, securities
  classification, etc.) — separate from this document's scope, and
  explicitly not something engineering hardening can substitute for.
- **The professional security audit itself** — this document and its
  siblings are preparation *for* that audit, not a replacement for it.
- **Mainnet ETH custodial key-management design** — the Sepolia-only
  corner built so far is covered by §3.13 above; moving any of this to
  mainnet (real value at stake) needs its own dedicated threat model and
  a hardware/KMS backend that's enforced rather than defaulted-to, not
  an extension of this section. See `KNOWN_GAPS.md`.

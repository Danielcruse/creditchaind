# Consensus Redesign Proposals: Permissionless Networking & Fork Rollback

**Status: PROPOSAL ONLY — nothing in this document has been implemented.** No
code files were modified while researching or writing this. This is a
technical design deliverable for the platform owner to review, approve,
redirect, or reject before any implementation work starts, per the explicit
scoping of this task. It is written to extend, not duplicate or contradict,
`audit/KNOWN_GAPS.md`'s existing entries on these two topics (see
"Consensus / Validator Security" and "Networking / P2P Permissioning" for
Problem 1's current disclosure, and the same "Consensus / Validator
Security" section's "No state rollback on reorg" entry for Problem 2's).
Every file:line citation below was re-verified directly against source
during this pass (2026-08-20), not carried over from memory.

---

## Problem 1: Permissionless networking / Sybil resistance

### Current state (re-verified against source)

- `api/peer.go:29-45` — `networkSecret()`/`requireNetworkSecret` gate every
  `/peer/*` route behind one static, network-wide shared secret
  (`NETWORK_SECRET`). Fails closed if unset.
- `api/peer.go:189-268` — `handshakeWith`/`HandlePeerHandshake` now require
  a proof-of-possession signature over the claimed NodeID/PublicKey/
  Address/Timestamp (the 2026-08-10 fix), closing identity *spoofing*. This
  costs an attacker who already holds `NETWORK_SECRET` nothing beyond
  generating a fresh Ed25519 keypair — free, unlimited, and undetectable as
  "the same actor" across repeated handshakes with different addresses.
- `core/peer.go:9-26,32-52` — `Peer`/`PeerStore` carry no stake, no fee,
  no cap on entries; `PeerStore.Add` (called from any successful handshake)
  will happily register an unbounded number of distinct `Peer` records for
  one `NETWORK_SECRET` holder.
- `api/peer.go:286-326,328-350` (`HandlePeerTx`, `HandlePeerBlock`) have no
  per-peer or per-identity rate limit at all — only the generic, route-
  agnostic per-IP token bucket (`api/hardening.go:16-70`, 20 req/s
  sustained / 40 burst) applies, and it doesn't distinguish a brand-new
  anonymous connection from an established, high-reputation validator's
  node.
- `core/peer.go:16-25,103-129` — `Peer.FailedAttempts`/`Suspended` plus
  `PeerStore.RecordFailure`/`RecordSuccess` already implement a basic,
  binary reliability score, but it's only ever fed by network/validation
  failures (unreachable, invalid chain) — never by rate-limit violations,
  and it carries no positive/graduated trust dimension, only "suspended or
  not."
- **The consensus-safety question is already solved, separately, and this
  proposal does not touch it.** `api/peer.go:458-478`
  (`verifyBlockProducer`) and `:506-517` (`verifyLeaderOrFallback`) already
  anchor block legitimacy to `core.ValidatorStore`'s on-chain-verified
  `AuthorizedNodePublicKey` — never to `peerStore`, exactly per the
  2026-08-10 fix and its regression test
  (`api/peer_security_test.go:34-73`,
  `TestVerifyBlockProducer_RejectsForgedSignatureEvenWithPoisonedPeerStore`).
  Likewise every transaction admitted to the mempool independently re-runs
  `verifyTxAuth` regardless of which peer relayed it (`api/peer.go:316`,
  the M-01 fix). So a flood of cheap Sybil "peers" cannot get *forged data
  accepted* — that's already closed. What it *can* do is waste this node's
  CPU (JSON decode + Ed25519 verify per rejected item, as KNOWN_GAPS.md's
  own framing of this gap already says) and bandwidth, and pollute
  `peerStore`'s topology/discovery graph. This is a resource-exhaustion /
  topology-integrity gap, not a data-integrity one — important for scoping
  the fix correctly.
- **Validator onboarding already has real Sybil-resistance economics**,
  entirely separate from the peer layer: `core/consensus.go:36`
  (`MinValidatorStake = 1,000` CCT), `api/apply.go:4219-4253`
  (`applyValidatorRegister`, gated on `validatorApplicationStore.IsApproved`
  plus `wallet.StakedCCT >= minValidatorStakeForCount(...)`), and the
  staged application pipeline documented in
  `api/validator_application.go:13-40`: Stage 1 requires support from
  *other* reputation-≥30 wallets (capped at 3 supported applications per
  wallet, `api/validator_application.go:70-71`), Stage 2 requires binding
  to a real, independently-running node that must itself get blocks
  accepted, Stage 3 requires the CCT stake. Spinning up N sybil validators
  costs N × 1,000+ CCT *and* requires convincing other, independent
  reputation holders to vouch for each one — genuinely expensive, not a
  shared string.
- `api/bridge_validator.go:19-46` (`BRIDGE_VALIDATOR_ADDRESSES`) and
  `api/admin.go`'s `ADMIN_MULTISIG_ADDRESSES` are a different pattern
  entirely: a small, operator-curated allowlist for a deliberately
  centralized, accountable signer set (vault custody, admin governance).
  The whole point of that pattern is *not* permissionless — it's the
  opposite end of the trust spectrum from what peer admission needs.

### Proposed design

**Reframe the gap accurately first.** Given that consensus-critical trust
(which blocks/transactions are real) is already independent of
`peerStore`, the actual design problem is two-layered:

1. **Admission** — who gets to open a `/peer/*` connection at all.
2. **Trust allocation** — how much bandwidth/rate-limit budget and
   sync-source weight an admitted peer earns.

The recommended design deliberately does **not** try to make admission
itself expensive (that fights "permissionless" directly) — instead it
makes admission free and open, and makes trust earned, mirroring how real
permissionless networks actually work:

- **Cosmos IBC light clients**: a relayer carrying packets between chains
  is permissionless and unprivileged — trust is anchored to the
  *counterparty chain's own validator-set signatures*, independently
  re-verified by the light client, never to the relayer's identity. A
  malicious relayer can withhold or delay, never forge. This is the
  closest real-world analogue to CreditChain's *already-existing* design:
  a gossiped block/tx is only ever accepted after independent
  re-verification, regardless of who relayed it.
- **Ethereum discovery protocol + eth/68**: fully open Kademlia-style
  discovery with zero admission cost, paired with per-peer, purely local
  reputation scoring at the application layer (eth/68) that disconnects or
  deprioritizes peers sending useless/invalid data. No shared credential,
  no stake requirement to just relay.
- **Bitcoin addr gossip**: also fully open; defended by per-connection ban
  scores and a hard cap on total connection count (default 125), not an
  admission fee.
- **How production PoS chains actually prevent Sybil peer floods**: none
  of the major ones (Ethereum consensus-layer p2p, Cosmos/Tendermint p2p)
  separately stake-gate the *gossip/relay* layer. Bonded, slashable stake
  gates the *validator/consensus-participant* set specifically — the
  layer with unilateral power to propose or attest. The gossip layer below
  it relies on open admission + local reputation/rate-limit scoring, the
  same shape as the discovery-protocol comparison above. CreditChain
  already has the validator-layer piece (`MinValidatorStake` + staged
  application). The genuinely missing piece is the gossip-layer local
  scoring, not a peer-level stake requirement.

**Concrete design, in three trust tiers, reusing existing machinery
wherever a fit exists:**

- **Tier 0 — Anonymous / unregistered (the actual "permissionless join").**
  Any reachable node may `/peer/handshake` and be admitted to `peerStore`
  with **no secret, no fee, no vetting** — this is what makes the network
  genuinely self-serve. It gets the smallest rate-limit budget (roughly
  today's existing global default, 20/40) and zero preferential weight as
  a chain-sync source.
- **Tier 1 — Registered relay (`TxPeerRegister`, new, lightweight).** A
  wallet pays a small, non-refundable CCT fee to bind one address to one
  NodeID/node-public-key pair, reusing the exact fee-deduction-at-apply
  idiom `applyValidatorRegister` already uses and the exact
  `AuthorizedNodeID`/`AuthorizedNodePublicKey` binding shape
  `ValidatorStatus` already carries (`core/validator_store.go:26-34`) —
  but as a new, separate, much lighter store (e.g.
  `PeerRegistrationStore`), **not** `ValidatorStore`, and **not** gated by
  the staged governance-application pipeline or `MinValidatorStake`.
  Forcing every relay operator through the full validator pipeline just to
  run a plain full node would suppress honest participation and defeat
  "permissionless" — the fee only needs to be large enough that minting
  thousands of identities is a real, felt cost, not that running one
  identity is expensive. Gets a medium rate-limit budget and counts toward
  sync-source preference.
- **Tier 2 — Validator-backed (free, fully reused from existing PoSR
  machinery).** Any address that is currently `Active` and clears
  `isEligible`/`isEligibleRelaxed` in `validatorStore`
  (`core/consensus.go:95-123`) is automatically trusted at the highest
  tier for its `AuthorizedNodeID` — no new mechanism needed, this is
  exactly the same anchor `verifyBlockProducer` already uses. A validator
  wanting sybil peer identities at this tier would need to clear the full
  validator pipeline N times, which is already the expensive, socially
  gated path described above.

**Rate limiting, specifically for `/peer/tx`/`/peer/block`:** a per-peer
token bucket, keyed by registered identity (NodeID/address) where known,
falling back to remote IP for genuinely unregistered connections —
layered **on top of**, not instead of, the existing global per-IP limiter
in `api/hardening.go` (IP-flood defense and identity-scoped defense catch
different attacks). Bucket size scales by tier (illustrative, to be tuned,
not derived from a model): Tier 0 ≈ today's 20/40; Tier 1 ≈ 50/100; Tier 2
≈ 200/400, loosely mirroring PoSR's own trust weighting rather than
inventing a second formula. **Gap in the existing reliability tracking**:
`PeerStore.RecordFailure` (`core/peer.go:103-116`) is currently only
called for network/validation failures, never for rate-limit rejections —
a peer that floods and gets throttled every request is never actually
`Suspended`. This should be closed as part of the same change: a
rate-limit violation should count toward `FailedAttempts` too.

### Interaction with existing allowlist patterns and validator machinery

- **Do reuse `ValidatorStore`/`MinValidatorStake`/the staged application
  pipeline directly for Tier 2** — no parallel registry, exactly mirroring
  how `verifyBlockProducer` already anchors to it.
- **Do not reuse `BRIDGE_VALIDATOR_ADDRESSES`/`ADMIN_MULTISIG_ADDRESSES`'s
  operator-curated-allowlist pattern for general peer admission** — that
  pattern exists specifically for small, deliberately centralized,
  off-chain-vetted signer sets, the opposite of what "permissionless"
  means. It remains useful narrowly as (a) a manual escape-hatch env var
  an operator can use to pin a specific known peer regardless of tier
  (mirroring `AUTHORIZED_NODE_REPORTER_PUBLIC_KEYS`'s existing "bootstrap
  accommodation" role, `api/apply.go`'s C-01 fix), and (b) the bootstrap
  allowlist during migration, below.
- **`TxPeerRegister`'s fee should not reuse `MinValidatorStake`** — that
  number is calibrated for a role with unilateral block-production power
  and real slashing exposure; a plain relay has neither, so a much smaller,
  separately-named constant is appropriate, flagged as a fresh judgment
  call in the same style as this codebase's other disclosed economic
  constants.

### What it would take to implement (rough scope)

- `core/peer_registration.go` (new) — `PeerRegistrationStore`, mirroring
  `core/validator_store.go`'s shape but without stake/eligibility tiers.
- `api/apply.go` — new `applyPeerRegister` function plus a fee constant;
  extend `verifyTxAuth`'s switch for the new tx type.
- `core/peer.go` — extend `Peer`/`PeerStore` with a trust-tier field and
  rate-limit bucket state (or a parallel map keyed the same way as
  `Peers`); extend `RecordFailure`'s callers to include rate-limit
  rejections.
- `api/hardening.go` — new per-peer bucket logic layered alongside the
  existing per-IP `allowRequest`.
- `api/peer.go` — `HandlePeerTx`/`HandlePeerBlock` call the new per-peer
  check; `syncChainFromPeer` gains tier-aware source preference (a DoS/
  efficiency improvement — recall this is not safety-critical, since
  `ValidateChain`+`verifyBlockProducer`+`verifyLeaderOrFallback` already
  make an adopted chain safe regardless of source).
- New test file mirroring `api/peer_security_test.go`'s style: registration
  fee deduction, tier assignment, per-tier bucket sizing, and a rate-limit
  violation actually escalating `FailedAttempts`/`Suspended`.

This is a moderate-sized, mostly additive effort — smaller than Problem 2,
since it reuses `ValidatorStore` wholesale for the highest tier and only
needs one genuinely new store (`PeerRegistrationStore`) plus rate-limiter
extensions.

### Migration path (NETWORK_SECRET is live on two real, independently-restartable deployments)

Following this codebase's own established migration idiom (the
`H01FixCutoverUnixSeconds`/`H03FixCutoverUnixSeconds` cutover-timestamp
pattern, `C-05`'s legacy-ID-preserving dual scheme, `ADMIN_TOKEN`'s
"coexistence window, not a permanent second door"):

1. **Phase 1 (no coordination needed, deployable unilaterally per node):**
   add per-peer rate-limit tiers and the `RecordFailure`-on-flood fix,
   still entirely behind `NETWORK_SECRET` exactly as today. This only
   changes how much a node trusts an *already-admitted* peer — it cannot
   disagree with a node that hasn't upgraded yet, since admission itself
   is unchanged.
2. **Phase 2 (additive, coexists with `NETWORK_SECRET`):** ship
   `TxPeerRegister` and Tier-2-via-`ValidatorStore` recognition as an
   **alternative** admission path — `HandlePeerHandshake` accepts a caller
   if *either* `NETWORK_SECRET` is valid *or* the caller supplies proof of
   a confirmed `TxPeerRegister`/active validator registration. A node that
   hasn't upgraded yet is unaffected: its peers keep using
   `NETWORK_SECRET`, which keeps working. No simultaneous flag day.
3. **Phase 3 (retire `NETWORK_SECRET`, operator opt-in, no fixed date):**
   only after Phase 2 has run stably across every currently-federated
   node, an operator can remove `NETWORK_SECRET` from their own config,
   making the stake/registration path the sole door — same "supersedes
   entirely" pattern this codebase already applied to
   `ADMIN_TOKEN→ADMIN_WALLET_ADDRESS→multisig`. Because Phase 2 was
   additive, each operator can do this at their own pace.
4. Today this project has one operator running both real nodes, so the
   "independently operated, can't coordinate" risk named in the prompt is
   currently more theoretical than actual — but the phased design above is
   still the right one to build, because it's what's actually required
   once third-party operators exist, and building it that way now avoids
   a second migration later.

### Explicitly out of scope / disclosed remaining gaps

- **The free/anonymous (Tier 0) admission tier has no Sybil resistance by
  design**, matching Ethereum discovery/Bitcoin addr gossip's own accepted
  model: an attacker can still open arbitrarily many low-trust anonymous
  connections. The mitigation is that doing so earns almost nothing (small
  fixed budget, no sync-source preference) — not that it's prevented
  outright.
- **`TxPeerRegister`'s fee amount is a fresh, undirected judgment call**,
  same disclosed-constant convention as `MissedBlockStakeSlashBps`,
  `DefaultProposalQuorum`, etc.
- **Peer reputation stays purely node-local, never replicated.**
  `PeerStore` today is per-node with no gossip/consensus layer of its own;
  a peer well-behaved toward node1 starts from zero trust with node2.
  Making peer reputation itself a replicated, consensus-agreed fact would
  need its own gossip sub-protocol with its own Sybil-resistance question
  one level up — explicitly not attempted here.
- **Eclipse-attack resistance (topology diversity) is not addressed.** A
  well-resourced attacker surrounding a victim node with only
  attacker-controlled peers isn't defended against by trust tiers alone;
  that needs outbound-connection diversity requirements (multiple
  independent introduction paths, geographic/ASN diversity), a further,
  separate hardening pass.
- **This proposal does not touch, and does not need to touch, the
  already-solved consensus-safety question** (which blocks/transactions
  are legitimate) — that's independently closed by the 2026-08-10
  `verifyBlockProducer` fix and the M-01/C-02/H-03 audit fixes. Nothing
  here should be read as implying today's chain is unsafe against a
  malicious peer's *data*; it is only inefficient against a malicious
  peer's *volume*.
- **Coupling risk**: peer trust tiering for Tier 2 derives directly from
  `isEligible`/`isEligibleRelaxed`/`isEligibleBootstrap`
  (`core/consensus.go:95-154`), which already has its own three-tier
  deadlock-avoidance fallback logic. A future change to those functions
  would silently change peer-trust tiering too — worth a dedicated
  regression test tying the two together explicitly if this is built.

---

## Problem 2: Fork resolution / state rollback on reorg

### Current state (re-verified against source)

- `core/replace_chain.go:47-68` (`CommonPrefixLen`) and its own doc
  comment already explicitly names the distinction this proposal
  addresses: "the candidate replaces some suffix we already applied (a
  reorg, which would require rolling back the state changes from our
  orphaned blocks — not implemented, see api/peer.go's
  syncChainFromPeer)."
- `api/peer.go:613-639` — the extension path (candidate chain cleanly
  extends the local tip) verifies and applies one block at a time using
  the same per-block checks (`verifyBlockProducer`, `blockWithinLimits`,
  `verifyLeaderOrFallback`) as live gossip adoption. **This part is
  already hardened and this proposal does not touch it.**
- `api/peer.go:582-602` — the divergence path: if `CommonPrefixLen`
  reports the candidate splits off before the local tip
  (`prefix < len(blockchain.Blocks)`), the node calls `recordForkEvent`
  and refuses outright. No rollback is attempted.
- `api/apply.go` alone defines 84 functions named `apply*`
  (`grep -c "^func apply" api/apply.go` = 84 during this pass); at least
  20 more are spread across `api/vault_lock.go`, `api/admin_multisig.go`,
  `api/bridge_tx_hash.go`, `api/tokenomics.go`, and roughly a dozen other
  files. **KNOWN_GAPS.md's own "~50" figure is, if anything, an
  undercount** — the real number is closer to 100+. Each mutates one or
  more package-level singleton stores in place, with no delta/undo
  capability.
- `api/persistence.go:296+` (`SaveState`) writes the *entire* current
  state wholesale to `state.json` after every applied block (both
  self-produced, via `produceBlockAsLeader`, `api/consensus_loop.go:250-261`,
  and synced, via `syncChainFromPeer`, `api/peer.go:641-642`). Only the
  single latest copy exists on disk for a `state.json`-only node — no
  history.
- `api/db.go:23-125` — **genuinely relevant existing infrastructure**: if
  `DATABASE_URL` is configured, every `SaveState()` call *also* inserts a
  full JSONB snapshot row into Postgres (`saveSnapshotToDB`,
  `:100-125`), and `dbSnapshotRetention = 20` (`:31`) keeps the last 20
  such rows per node before pruning. This is live today **only on
  mainnet** (per KNOWN_GAPS.md's "Postgres dual-write is now live on
  mainnet" entry — node1/testnet does not have `DATABASE_URL` set). It was
  built for disaster recovery (restoring a node that lost its disk
  entirely), not reorg rollback, and currently carries no
  block-index tag — only `created_at`.
- `api/consensus_loop.go:11,24` — `consensusTickInterval = 8s`,
  `missedRoundsBeforeFallback = 6` (48s fallback-deadlock-breaker window).
  Both matter for sizing a max-reorg-depth policy in real wall-clock
  terms.
- `api/vault_lock.go:259-338` (`applyVaultLockAttest`) and `:767-785`
  (`applyVaultReleaseComplete`) are the two sharpest points of risk named
  in the prompt. Both are idempotent by design (re-applying an already-
  minted/released record is a documented no-op), but neither has any
  concept of "wait N confirmations on CreditChain's own chain before
  treating this as final" — they act the instant a confirming transaction
  lands in *any* applied block, at any depth.
- `api/vault_info.go:91,115` — `vaultEthConfirmations()`/
  `vaultBtcConfirmations()` exist but answer a **different** question: how
  many confirmations on the *external* (Sepolia/Bitcoin) chain a validator
  waits for before attesting at all. They say nothing about how deep an
  already-applied bridge transaction needs to sit in *CreditChain's own*
  chain before it's safe to treat as irreversible for rollback purposes.

### Proposed design

**Feasibility assessment first, since the prompt specifically asks for
honesty about what's realistic given this architecture (single global
mutex-guarded package-level stores, not a merkle/verkle state model):**

- **Option A — true per-transaction undo log.** Would require every one of
  the 100+ `apply*` functions to record a reversible delta (or a generic
  before/after capture around every store mutation). This is the more
  "correct" academic answer, but for this codebase specifically it is
  disproportionately risky: KNOWN_GAPS.md already flags
  `resetApplyTestState()` — the only existing "reset everything" routine —
  as first-party evidence that enumerating every store correctly is
  already error-prone here. An undo-log approach makes that enumeration
  problem *permanent and load-bearing* — every apply function added in the
  future would also need to correctly implement its own undo, forever, or
  the mechanism silently rots. **Not recommended as the first move.**
- **Option B — periodic full-state snapshot + restore + replay-forward.**
  **Feasible without a full state-model rewrite**, and largely already has
  its low-level primitives in place: `SaveState`/`LoadState` already
  perform a faithful full-state serialize/deserialize round-trip on every
  single node restart today. What's actually missing is narrower than it
  looks: (1) keeping *multiple* historical full snapshots instead of
  overwriting one file — Postgres already does exactly this, today, on
  mainnet; (2) tagging each snapshot with the block index/hash it
  corresponds to; (3) a restore-then-replay orchestration, which can reuse
  the *existing* per-block verify/apply loop (`api/peer.go:613-639`)
  almost as-is rather than inventing new state-mutation logic. **This is
  the recommended path.**

**Concrete design (Option B):**

1. **Schema**: add a `block_index BIGINT` column to `state_snapshots`
   (`api/db.go:76-84`), populated from `blockchain.LatestBlock().Index` at
   every `SaveState()` call (the value is already available there). Split
   retention policy in two: keep the existing `dbSnapshotRetention = 20`
   short rolling window for its original disaster-recovery purpose, and
   add a **separate** longer-retention checkpoint set — one snapshot every
   `checkpointIntervalBlocks` (illustrative: 500 blocks ≈ ~1.1 hours at
   the current 8s tick), retained for a bounded window (e.g. 30 days).
   Conflating the two retention policies risks either pruning a
   disaster-recovery copy too early or letting the checkpoint table grow
   unbounded — they serve different purposes and should be governed
   separately.
2. **Hard prerequisite, stated plainly**: this entire mechanism depends on
   Postgres. A `state.json`-only node (node1/testnet, today) has no
   history to restore from and **cannot participate in rollback under this
   design** until `DATABASE_URL` is provisioned for it — it keeps exactly
   today's refuse-and-alert behavior, forever, until then. This is a new,
   real operational dependency this proposal introduces, not an
   incidental detail.
3. **Rollback procedure**, replacing the unconditional refusal at
   `api/peer.go:582-602`:
   - Compute `prefix` (already done, via `CommonPrefixLen`).
   - **Enforce max-reorg-depth first** (see below) — if
     `len(blockchain.Blocks) - prefix` exceeds policy, refuse exactly as
     today. A rollback *mechanism* existing does not mean arbitrarily deep
     reorgs get accepted; this is a deliberate, permanent ceiling, not a
     stepping-stone to removing it later.
   - **Enforce the bridge-transaction hard rule** (see below) — if any
     already-applied block between `prefix` and the current tip contains
     one of the designated bridge transaction types, refuse regardless of
     depth.
   - Locate the newest retained checkpoint at or before block `prefix`. If
     none exists (checkpoint interval too coarse relative to the actual
     divergence — a real, disclosed edge case below), refuse and alert
     exactly as today.
   - Restore all package-level stores from that checkpoint's serialized
     blob, reusing `LoadState`'s existing parsing logic (this needs a
     small, mechanical refactor: splitting "read `state.json` off disk"
     from "parse state bytes into the package-level stores," so a
     Postgres-sourced blob can go through the identical parse path).
   - Build the restored state and replayed chain **in a working, non-live
     copy** first; only after every block from checkpoint+1 through the
     candidate's new tip passes the *exact same* verify+apply sequence
     `syncChainFromPeer`'s extension path already runs
     (`verifyBlockProducer`/`blockWithinLimits`/`verifyLeaderOrFallback`/
     `ApplyBlock`, with zero failures) does the node commit: atomically
     swap the restored stores in for the live package-level singletons,
     persist `state.json` plus a fresh snapshot, and resume. A replay
     failure partway through must leave the pre-rollback state completely
     untouched — this is exactly the "atomically swap in only after a
     full successful replay" approach KNOWN_GAPS.md's own Phase 2 sketch
     already named; this proposal makes it concrete, not different.
   - **Side effects must be suppressed during replay**: `gossipBlock`
     re-firing during replay is merely wasteful (receivers already have
     these blocks), but anything that talks to a *real external chain*
     (the vault release relayer, bridge deposit pollers) must not
     re-trigger as a replay side effect — see the bridge section below for
     why this specifically cannot be allowed to happen twice.
4. **The real engineering weight here is not the ~100 `apply*`
   functions** — good news, since none of them need to change. It's
   correctly enumerating **every distinct package-level store variable**
   (`walletStore`, `validatorStore`, `stakingStore`, `delegationStore`,
   `loanStore`, `treasuryStore`, `vaultLockAttestationStore`,
   `vaultReleaseApprovalStore`, `mempool`, and however many others exist)
   so the atomic-swap step touches every one of them, correctly, with none
   missed — precisely the enumeration risk KNOWN_GAPS.md already flags via
   `resetApplyTestState()`. This enumeration should be the **first**
   implementation step, before any rollback logic is written.

### Max reorg depth policy

`consensusTickInterval = 8s`, `missedRoundsBeforeFallback = 6` (48s)
together bound how long a genuine, honest network partition can plausibly
run before the network's own existing fallback/sync mechanisms
(`trySyncBeforeFallback`, `api/consensus_loop.go:223-229`) would already
have started healing it. A legitimate partition-healing reorg should
rarely need to reach past roughly one fallback-window's depth on the
losing side. For comparison: Bitcoin implementations commonly alarm on
reorgs beyond ~6 confirmations (~1 hour); post-merge Ethereum has
economic (Casper FFG) finality making a finalized block literally
irreversible without a ⅓+-stake slashing event, and pre-merge commonly
alarmed past ~100 blocks. CreditChain has neither a BFT finality gadget
nor Bitcoin's proof-of-work cost-to-attack, so its policy should be sized
against its *own* block time and validator set, not borrowed wholesale
from either. Proposed: `maxReorgDepthBlocks` (illustrative judgment call,
to be tuned, not derived from a model) ≈ 20 blocks (~160s, a little over
3× the existing fallback window) as the default automatic-rollback
ceiling. Beyond that, refuse and require manual operator intervention
exactly as today — deliberately, since a reorg that deep is now more
likely to indicate a bug or attack than an honest partition, matching
Bitcoin/Ethereum's own precedent of treating "too deep" as "stop and let a
human look," not "handle it more cleverly."

### Interaction with the vault bridge — the critical safety requirement

`TxVaultLockAttest`, `TxVaultReleaseComplete`, and the same "node-authored
report" family the C-01 fix already enumerates (`TxBridgeDeposit`,
`TxBridgeBtcDeposit`, `TxBridgeTxHashRecord`) represent **external,
already-irreversible real-chain facts**. Once `applyVaultReleaseComplete`
has confirmed a real Sepolia/Bitcoin broadcast happened, no CreditChain-
side rollback can un-send it — reverting `approval.Status` back to
`Pending` risks a since-real-and-gone release getting re-approved and
re-relayed against a transaction that already, unstoppably, happened. This
is the single most dangerous failure mode a rollback mechanism could
introduce, and it's addressed two ways, belt-and-suspenders, per the
prompt's own framing of the options:

1. **Exclusion from rollback eligibility (the primary mechanism, chosen
   over "extra confirmation depth" alone).** If a candidate rollback's
   divergence point is at or before any already-applied block containing
   one of the bridge transaction types above, the rollback is refused
   **unconditionally**, regardless of `maxReorgDepthBlocks`. Reusing the
   exact enumeration `isAuthorizedNodeIdentity`'s "node-authored report"
   list already defines (`api/apply.go`, C-01 fix) keeps the two lists
   from drifting apart over time. This is a hard invariant rather than a
   deeper-but-still-finite probabilistic threshold, because the failure
   mode is "real money irreversibly sent" — the risk tolerance for that
   should be zero, not "very unlikely."
2. **Extra confirmation depth before acting externally (defense in
   depth, independent of whether rollback ever ships).** A new
   `bridgeFinalityDepthBlocks` constant, larger than
   `maxReorgDepthBlocks` (illustrative: 2-3×), should gate the moment the
   vault release relayer actually broadcasts a real external transaction,
   or a bridge deposit report is treated as safe to rely on — waiting
   until the confirming CreditChain block is at least
   `bridgeFinalityDepthBlocks` behind the current tip. This is a *new*
   depth check, distinct from `vaultEthConfirmations()`/
   `vaultBtcConfirmations()` (which gate attestation against the
   *external* chain, not action against CreditChain's *own* chain depth).
   Net effect: a bridge transaction is only ever at real risk during the
   short window between being applied and crossing
   `bridgeFinalityDepthBlocks` — during that window the relay/broadcast
   side deliberately hasn't acted yet, so even a rollback that *does*
   touch that block moves no real external money. Worth flagging as a
   pre-existing timing gap independent of this proposal: the relay
   appears to act as soon as an approval reaches threshold today, with no
   CreditChain-side confirmation wait at all.
3. **The M-of-N attestation vote count is not separately at risk.**
   `vaultLockAttestationStore`'s per-event attestor map is ordinary
   package-level state, correctly rebuilt by the same checkpoint-restore
   step as everything else, and attestation application is already
   idempotent (`api/vault_lock.go:301-306`) — replaying a different
   (post-fork) sequence of attestation transactions legitimately produces
   a different, equally valid vote count for an event that hasn't yet
   crossed the mint/release line. The special protection needed is
   specifically about the *outcome* crossing irreversibility (rule 1
   above), not the vote-counting mechanics, which are safe under replay by
   construction.

### What it would take to implement (rough scope)

- `api/db.go` — schema change (`block_index` column), split checkpoint vs.
  disaster-recovery retention policies.
- `api/persistence.go` — decouple `LoadState`'s disk-read from its
  byte-parsing so a Postgres-sourced snapshot blob can go through the
  same path; `SaveState` passes block index through to
  `saveSnapshotToDB`.
- `api/peer.go` — `syncChainFromPeer`'s divergence branch
  (`:582-602`) gets real rollback logic in place of the unconditional
  refusal.
- New file, e.g. `api/reorg_rollback.go` — checkpoint lookup, restore,
  bounded replay, atomic commit orchestration, plus the
  `maxReorgDepthBlocks`/`bridgeFinalityDepthBlocks` constants and the
  bridge-tx-type exclusion list (shared with C-01's existing enumeration).
- **A first, dedicated step to enumerate every package-level store
  variable** that must be captured/restored — this is prerequisite work,
  not optional, and its output determines the real size of the atomic-swap
  step.
- `api/vault_release_relay.go` and the bridge deposit-report submission
  paths — add the `bridgeFinalityDepthBlocks` wait-before-acting check.
- `api/fork_monitor.go` — extend `recordForkEvent`/`forkStatus` to
  distinguish "refused, too deep," "refused, would touch an irreversible
  bridge transaction," and "rolled back successfully" as three distinct
  states on `GET /health/sync`, rather than collapsing them into one
  "fork detected" signal — operators need to know which happened.
- New test suite mirroring the rigor of `core/consensus_posr_fixedpoint_test.go`/
  `api/c02_leader_election_test.go`: successful shallow rollback+replay
  reproduces identical final state to a node that had been on the winning
  fork all along; a too-deep rollback is refused; a rollback crossing an
  applied bridge transaction is refused regardless of depth; a mid-replay
  failure leaves pre-rollback state completely untouched; the
  checkpoint/restore path is exercised against a real or mocked Postgres
  (worth flagging: `api/db.go` doesn't currently appear to have its own
  test file, an existing gap this work would inherit, not introduce).

**Rough sizing**: genuinely large, multi-week effort — not a quick patch,
matching KNOWN_GAPS.md's own framing that this "needs its own dedicated
review pass." The encouraging finding from this investigation: because
`SaveState`/`LoadState` already perform a full, exercised-every-restart
serialize/deserialize round-trip, and Postgres snapshot infrastructure is
already live on mainnet, this design reuses existing machinery rather than
inventing new state-capture logic — smaller in code volume than the
per-transaction undo-log alternative, but the store-enumeration and
atomicity work is still real, non-trivial engineering, not a thin wrapper.

### Migration/rollout considerations

- Purely additive to the already-hardened clean-extension path — no change
  to today's common-case behavior at all.
- Hard Postgres prerequisite means this is **effectively mainnet-only
  until node1 gets `DATABASE_URL` provisioned** — no regression for
  node1 either way (it keeps today's refuse-and-alert behavior exactly),
  but also no benefit there until that's done.
- Should be proven on testnet first and for a real stretch of wall-clock
  time before mainnet — "silently corrupting state" was the explicit
  reason `syncChainFromPeer` refuses today rather than guessing
  (`api/peer.go`'s own comment), so a bug in this mechanism is maximally
  dangerous; this is exactly the kind of change that shouldn't go live the
  same week it's written.
- A compiled-in feature flag (mirroring `DepositsHeldForSecurityRemediation`'s
  "compiled-in default, not a runtime toggle" pattern, or `MAINNET_MODE`'s
  env-gate) should gate whether automatic rollback is attempted at all vs.
  falling back to today's refuse-and-alert — enabled on testnet first,
  proven against real forks (this project has already had three), only
  then considered for mainnet, likely much later and as its own separate
  decision.

### Explicitly out of scope / disclosed remaining limitations

- **Per-transaction undo log (Option A) remains not attempted.** The
  checkpoint+replay approach is a real, working alternative for this
  specific architecture, but rollback granularity is bounded by checkpoint
  interval — a worst-case rollback could mean replaying up to
  `checkpointIntervalBlocks` blocks of computation (≈500 at the proposed
  interval), almost certainly fine but worth confirming empirically once
  built, not assumed.
- **Nodes without Postgres get zero rollback capability, permanently,
  under this design** — not a temporary gap, a structural one. This is a
  meaningful new operational dependency the platform owner should weigh
  explicitly, not an incidental detail.
- **No formal BFT finality is introduced.** `maxReorgDepthBlocks`/
  `bridgeFinalityDepthBlocks` are policy heuristics sized against this
  network's own block time and validator count, not a cryptoeconomic
  guarantee. A sufficiently large fraction of colluding/compromised
  validators could still force a divergence deep enough that the policy
  (correctly, by design) refuses to auto-heal it — requiring the same
  manual operator intervention as today, just at a larger depth threshold.
- **Does not address the root cause of the two real forks this project
  has already had** (both were node1/node2 divergences during OOM/
  instability incidents, not attacker-driven). A rollback mechanism helps
  the network recover *after* a fork; it doesn't reduce how often one
  happens. The stability fixes already applied this session (node1's RAM
  resize, health-check monitoring) are the actual prevention-side
  mitigation and are separate from this proposal.
- **The exact enumeration of every package-level store requiring
  capture/restore is unfinished work as of this proposal** — it's named
  above as the necessary first implementation step precisely because it
  isn't done yet; until it is, no fully precise file-list/line-count
  estimate is possible, only the shape given here.
- This proposal does not interact with Problem 1's rate-limiting design
  beyond both touching `api/peer.go` — noted only to avoid the reader
  assuming a dependency that doesn't exist.

# CreditChain — Full-Scale Security Audit Program

Nine-stage audit program, scoped specifically to this codebase's current
state. This is a roadmap and cross-reference index, not a replacement for
the detailed material already in `audit/` — each stage below points to
the specific documents, files, and open findings an auditor working that
stage should start from.

**Read this first, not as background: mainnet is already live.** There is
a real validator producing blocks, a real bridge contract holding real
ETH/BTC/USDC, and real registered bridge validators today — this is not a
pre-launch checklist for a protocol that hasn't shipped. That changes one
thing about the sequence below: Stage 3 (Bridge) cannot wait behind
Stages 1–2 on a strict calendar the way it could for an unlaunched
protocol. Run it in parallel with Stage 1. Everything else in the
proposed order holds.

```
Protocol Design ──┐
                   ├──> (parallel) Bridge  ─── highest urgency, real value exposed now
Cryptography ──────┘
        │
        v
Smart Contracts
        │
        v
Economic Model
        │
        v
Infrastructure
        │
        v
Penetration Testing
        │
        v
Public Testnet
        │
        v
Independent External Audit
        │
        v
Mainnet Readiness Sign-off
```

Live deployments in scope: mainnet (`3.39.124.210` / `service.creditchain.site`,
real value) and node1/testnet (`3.11.72.137` / `creditchain.site`, no real
value — already running publicly, doubling as the Stage 8 testnet
environment rather than something to stand up from scratch).

---

## Severity model

Every finding gets one of these, used consistently across all nine stages:

- 🔴 **Critical** — can steal funds, mint unbacked assets, or break consensus.
- 🟠 **High** — can disrupt the network or cause major financial loss.
- 🟡 **Medium** — significant bug or design weakness, no immediate catastrophic impact.
- 🟢 **Low** — minor issues, optimization opportunities, documentation gaps.

---

## Stage 1 — Protocol Architecture Audit

**Start here:** `audit/ARCHITECTURE.md` (system layout, transaction-authenticity
model, consensus/networking, token model) and `audit/THREAT_MODEL.md`
(actors, assets, attack surfaces, current mitigations).

Review, with the exact code each maps to:

| Area | Where it lives |
|---|---|
| PoSR consensus (stake + reputation + uptime weighted leader selection) | `core/consensus.go` |
| Validator incentives, slashing | `core/slashing.go`, `core/staking_logic.go` |
| CCT economics (supply cap, issuance schedule, fees) | `api/apply.go` (block reward logic), `core/governance_params.go` |
| Trust Score Index (formerly flat Reputation) — 6-dimension trust, governance-adjustable weights | `core/trust.go`, `api/reputation_math.go` (live-computed explainer, not hand-typed docs) |
| Fee collection and distribution | `core/platform_fee.go`, fee-application call sites in `api/apply.go` |
| Treasury | `api/treasury.go` |
| Bridge model (design level — see Stage 3 for implementation-level review) | `audit/ARCHITECTURE.md` §bridge, `contracts/AUDIT_PACKAGE.md` |
| Lending and liquidation | `core/credit_engine.go`, `core/liquidation.go` |

**Known prerequisite gap**: economic constants (unbonding period, dormancy
period, reward weights, most fees) are still at explicitly-disclosed
demo-scale placeholder values, not tuned for real production economics —
see `audit/economic_constants_recommendations.md`. "Prove the protocol
cannot become economically insolvent under expected conditions" is hard
to answer meaningfully until real numbers replace these. Treat retuning
these as a likely prerequisite deliverable out of this stage, not
something the audit can route around.

**Output**: a document proving the protocol cannot become economically
insolvent under expected conditions — see also Stage 5, which tests the
same question from the incentive-manipulation angle rather than the
static-parameter angle.

---

## Stage 2 — Cryptography Audit

Review:

| Area | Where it lives |
|---|---|
| Wallet generation, recovery phrases (BIP-39), key derivation | `website/js/hdwallet.js`, `website/js/vendor/hd-crypto.js` (client-side, in-browser — private keys never reach the server) |
| Transaction signatures | Ed25519, `core/request_auth.go` — self-certifying addresses (`address = SHA256(pubkey)[:20]`) |
| Validator signatures (block production) | `core/consensus.go`, `api/peer.go`'s `verifyBlockProducer` |
| Threshold/multisig logic | `contracts/CreditChainVault.sol` (`ecrecover`-based on-chain threshold — see Stage 3 for the current threshold=1 finding), `api/admin_multisig.go` (internal governance multisig, separate credential from the vault's) |

**Goal**: ensure nobody can forge ownership or signatures. Note the
codebase uses two distinct signature schemes for two distinct purposes —
Ed25519 for CreditChain's own protocol (wallets, blocks, validator
identity) and secp256k1/ECDSA for the Ethereum-side vault contract. These
don't share a trust boundary and should be reviewed as two separate
cryptographic surfaces, not one.

`core/request_auth_test.go` (9 tests) already covers signed-envelope
verification — a starting point for what's already been self-tested, not
a substitute for independent review.

---

## Stage 3 — Bridge Audit (Highest Priority — Real Funds Exposed Today)

**Start here:** `contracts/AUDIT_PACKAGE.md` (the existing smart-contract-side
audit prep for `CreditChainVault.sol`) and `audit/creditchain-audit-2-scope.md`
§1 (the currently-open critical finding, detailed below).

Review:
- Deposit verification — `TxVaultLockAttest` handling in `api/apply.go`,
  fed by the watch loops `api/vault_eth_watch.go`/`api/vault_btc_watch.go`
- Minting — mint happens only once attestation threshold is met (see the
  finding below on what that threshold actually is today)
- Burning — `api/vault_lock.go`'s `applyVaultBurn`
- Withdrawal / release — `api/vault_lock.go`'s `applyVaultReleaseApprove`,
  `api/vault_release_approve_watch.go`, `api/vault_release_relay.go`
- Reserve accounting — **no automated on-chain-vs-off-chain reconciliation
  currently exists**; see condition 4 below
- Replay protection — per-approval-ID dedup, plus a 6–10 block
  confirmation-depth gate (`core.VaultReleaseConfirmationDepthBlocks`)
  added specifically to prevent a short CreditChain-side reorg from
  double-triggering a release
- Double-release prevention — same dedup mechanism
- Validator approval flow — `api/bridge_validator.go`'s
  `isBridgeValidatorAddress`

### The invariant, and where CreditChain stands against it today

The four conditions every release should satisfy:

1. **The wrapped asset has been burned** — ✅ built. `TxVaultBurn` must
   confirm on-chain before a release approval exists at all.
2. **A unique withdrawal ID hasn't been processed before** — ✅ built.
   Deduplicated per approval ID; the confirmation-depth gate additionally
   guards against a reorg-driven double-trigger.
3. **A validator threshold approves the release** — ❌ **not actually
   true today.** The deployed `CreditChainVault.sol` at
   `0xE3C27466943903B390d06Cddb265263091b2d752` has an on-chain
   `threshold()` of **1**, verified directly against the live contract
   via three independent public RPC providers (not just backend config —
   the backend's `BRIDGE_VALIDATOR_THRESHOLD=1` matches, both layers
   agree). Two validators are registered; either one alone can currently
   sign a `release()` moving the vault's entire real balance, or attest
   to a deposit that never happened, minting unbacked CCC1. Full detail:
   `audit/KNOWN_GAPS.md` → "CRITICAL, STILL OPEN: mainnet vault
   contract's on-chain threshold is 1" (2026-08-19/20). **This is the
   single highest-priority finding in this entire program.**
4. **The reserve balance remains fully backed after the release** —
   ⚠️ not independently verified anywhere today. There is no automated
   check that locked-BTC = BTC_CCC1 supply (and the equivalent for
   ETH/USDC) holds after every release. Recommend building this as a
   real, continuously-run invariant check (not just an audit-time
   manual query) — flag as a Stage 3 deliverable, not only a Stage 3
   finding.

**Why raising `threshold` alone won't fix condition 3**: it only adds
real security if the additional signers are genuinely independent,
separately-held keys. If the same operator can produce multiple
signatures, a higher threshold changes nothing against compromise or
coercion — see the full reasoning and the discussed mitigation paths
(genuinely independent second signer, withdrawal delay/veto window,
velocity caps, duress signals, and the longer-term direction: a
proof-based/light-client bridge removing human signers from the mint
path entirely — a working Helios light-client proof-of-concept already
exists, verified live against mainnet, see `KNOWN_GAPS.md`) in
`audit/creditchain-audit-2-scope.md` §1.

---

## Stage 4 — Smart Contract Audit

There is exactly one deployed smart contract in this project:
`contracts/CreditChainVault.sol` (Ethereum mainnet). Lending, staking,
pools, treasury, governance, and liquidation are **not** smart contracts
— they're CreditChain's own protocol logic (Go, `core/`/`api/`), covered
under Stage 1, not Solidity.

Review `contracts/CreditChainVault.sol` for:
- Reentrancy
- Integer issues
- Access control (`onlyValidator`-equivalent gates, `ecrecover` binding)
- Upgradeability (none currently — confirm this is the intended design,
  not an oversight)
- Flash-loan resistance (lower relevance here since the contract doesn't
  expose a loan primitive itself, but confirm nothing in the
  deposit/release flow can be manipulated within a single block/tx)

`contracts/AUDIT_PACKAGE.md` already documents the trust model and design
intent in detail — read it before the source, not after.

---

## Stage 5 — Economic Audit

This tests incentives, not code — the questions a static read of the
source won't answer on its own:

- Can validators manipulate rewards? (relevant code: `core/consensus.go`
  leader selection, `api/apply.go` block reward distribution)
- Can CCT be inflated faster than backing grows? (CCT itself has no
  external "backing" in the collateral sense — this question is really
  about whether issuance outpaces the network's real economic activity;
  cross-reference Stage 1's insolvency-proof deliverable)
- Can pools be drained? (`core/liquidity_pool.go` — constant-product AMM,
  0.3% fee)
- Can REPT/Trust Score be farmed dishonestly? (see the full mapping of
  all 19 real trust-moving mechanisms and which dimension each affects
  in `api/reputation_math.go` — this is generated live from the actual
  constants, not hand-maintained documentation, so it can't drift out of
  sync with what really happens on-chain)
- Can users extract more value than they contribute? (loan default
  economics — `core/credit_engine.go`; P2P dispute economics —
  `api/apply.go`'s dispute-fault-choice mechanism)

---

## Stage 6 — Infrastructure Audit

Review:
- Node software — `creditchaind` binary (this repo), `cmd/staticserve`
  (website hosting, separate process/service from the node)
- RPC endpoints — no JSON-RPC surface (CreditChain isn't EVM-compatible);
  review the REST API surface instead, `api/server.go`'s full route table
- Explorer — `api/explorer.go`, `website/explorer.html`
- APIs — see `audit/ARCHITECTURE.md`'s auth model (self-certifying signed
  envelopes, no API key/registration required)
- Databases — Postgres dual-write is live on mainnet (`api/db.go`,
  `DATABASE_URL` configured); `state.json` remains authoritative for
  reads, Postgres is a durability target only, not yet a read path
- Monitoring — local health-check (process/disk/stall/fork detection)
  deployed and active on node1; confirm current status on mainnet
  specifically, and note external heartbeat monitoring / CDN / WAF have
  been explicitly deferred (third-party account creation, outside what
  this project's own tooling does unassisted) — flag as a real
  operational gap for this stage, not a closed item
- Backups — confirm current backup cadence/verification for both nodes'
  `state.json`/`blockchain.json`
- Key management — bridge validator keys and the admin wallet key
  currently live as plain systemd environment files on the mainnet host,
  not HSM/hardware-wallet-backed. Flag explicitly: this is a real
  operational risk given these keys now authorize real fund movement,
  separate from the on-chain threshold finding in Stage 3.

**Known drift risk**: `audit/README.md`'s own "what's not ready" section
predates several changes made since (Postgres is now live; node2's
status may have changed). Verify current state directly rather than
trusting that document's specific claims — it documents a point in time,
not a live status page.

---

## Stage 7 — Penetration Testing

Attempt to attack, thinking like an attacker rather than a developer:

- **Wallet** — client-side key generation/signing (`website/js/hdwallet.js`);
  is there any path for a malicious page/script to exfiltrate a key that
  never leaves the browser by design?
- **API** — the full signed-envelope auth model; replay, timestamp-window
  bypass, cross-endpoint payload confusion
- **Bridge** — see Stage 3's four-condition invariant; attempt to violate
  each condition directly against a real testnet deployment
- **Trading Hub** — `api/hub_kyc.go`'s encrypted-at-rest certificate
  storage, `core.HubWorker`/agent-application routing
- **Validator portal** — `api/validator_application.go`'s staged
  onboarding pipeline (governance support → emailed confirm token →
  stake + register)
- **Governance** — proposal creation/voting weight manipulation,
  `api/governance_weight.go`

---

## Stage 8 — Testnet Audit

CreditChain already has a running public testnet — `creditchain.site`
(node1, `3.11.72.137`) — rather than something to stand up from scratch.
It is not currently run as a formal incentivized program; turning it
into one (public invitation, structured bug-bounty-style incentives for
wallet creation / bridging / becoming a validator / borrowing / lending /
attacking the protocol) is the remaining work for this stage, not the
infrastructure itself.

Fix every issue found before proceeding to Stage 9.

---

## Stage 9 — Mainnet Readiness Audit

Because mainnet is already live, treat this stage as a **re-certification
gate**, not a pre-launch gate. Before signing off that current mainnet
operation is acceptable pending remediation of open findings, confirm:

- [ ] All Critical findings from Stages 1–7 resolved, especially Stage 3's
      threshold finding
- [ ] Emergency pause procedures documented (currently: none exist for
      the vault contract — confirm whether this is an intended design
      choice or a genuine gap)
- [ ] Incident response plan ready and written down (not just tribal
      knowledge from past incidents — `audit/HARDENING_HISTORY.md`
      records what happened in past incidents, but not a forward-looking
      response plan)
- [ ] Backups verified (see Stage 6)
- [ ] Validator onboarding documented (`api/validator_application.go`'s
      pipeline is real and working — confirm the docs match the code)
- [ ] Monitoring and alerts active (see Stage 6's gap on external
      monitoring specifically)

---

## How to use this document

Stage 3 is the item that most warrants immediate independent judgment —
it's real, live, and financially exploitable today, at whatever value the
vault currently holds. Every other stage matters, but none of them are
sitting on an active, disclosed, unfixed critical finding the way Stage 3
is. Start there, run Stages 1 and 2 in parallel with it, and let Stages
4–9 follow the sequence above.

This document indexes into `audit/ARCHITECTURE.md`, `audit/THREAT_MODEL.md`,
`audit/KNOWN_GAPS.md`, `audit/HARDENING_HISTORY.md`,
`audit/creditchain-audit-2-scope.md`, `audit/economic_constants_recommendations.md`,
and `contracts/AUDIT_PACKAGE.md` — read those for full detail on any
specific item; this document is the map, not the territory.

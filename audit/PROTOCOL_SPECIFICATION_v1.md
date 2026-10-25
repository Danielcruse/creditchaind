# CreditChain Protocol Specification v1.0

**Status:** Living document, v1.0, dated 2026-10-25.
**Purpose:** A single, auditor-readable reference for the real rules and
exact values this protocol actually runs on today - pulled directly out of
the ~40+ Go files and the much longer `audit/KNOWN_GAPS.md` that previously
scattered this information. Every number below is a real constant in the
code, named by file, not an aspirational target. Where a rule has a known,
disclosed gap, this document says so plainly and points at
`audit/KNOWN_GAPS.md` for the full writeup rather than duplicating it.

This document describes the rules as they exist in code at the time of
writing. It is not a promise that any value stays fixed - several are
explicitly governance-adjustable (see §6), and this document should be
re-verified against source before being relied on for a specific line
number.

---

## 1. Consensus (Proof of Stake and Reputation - "PoSR")

Locked parameters, approved 2026-07 (`core/consensus.go`).

A validator must clear ALL of these to enter the weighted-random selection
pool at all:

| Gate | Value |
|---|---|
| Minimum stake | `MinValidatorStake` = 1,000 CCT |
| Minimum uptime | `MinValidatorUptime` = 0.90 |
| Minimum reputation | `MinValidatorReputationGate` = 20 |

Among eligible validators, selection weight is a fixed blend - deliberately
reputation-dominant, not wealth-dominant ("Proof of Stake and Reputation,"
not "Proof of Richest"):

| Component | Weight |
|---|---|
| Stake (normalized, capped at `StakeCap`=100,000 CCT so no single whale can dominate through wealth alone) | 25% |
| Reputation | 55% |
| Uptime | 20% |

Selection weight arithmetic is integer fixed-point (scale 1,000,000), not
float64/`math.Pow` - this is a deliberate cross-platform-determinism choice
(`core/consensus.go`'s own doc comment): IEEE-754 float ops are bit-identical
across platforms, but `math.Pow` is not, and validator selection must be
byte-for-byte reproducible on every node regardless of OS/architecture.

**Slashing table** (`core/slashing.go`, locked 2026-07):

| Offense | Reputation penalty | Strike weight | Stake slash |
|---|---|---|---|
| Missed block | -2 | 1 | 0.50% of staked CCT (burned, not redistributed) |
| Double-sign | -50 | 3 | 10.00% of staked CCT (burned) |

A validator is **permanently banned** (can never register or be selected
again) once accumulated strikes reach `PermanentBanSlashCountThreshold` = 6
- e.g. two double-signs, or one double-sign plus three missed blocks, or six
missed blocks alone. Double-sign slash reports are independently
re-verified from embedded cryptographic evidence before being applied.
Missed-block reports have no equivalent proof (absence can't be
cryptographically demonstrated), so as of 2026-07-20 they instead require
M-of-N corroboration from other currently-active validators rather than
trusting a single node's word - see `audit/KNOWN_GAPS.md` for the full
history of that fix.

**Block reward**: `pendingBurnBasedBlockReward`/`applyBlockReward`
(`api/apply.go`) pay out 90% of `core.Treasury.CCTBurnedPendingReward` -
i.e. the reward pool is funded by CCT genuinely burned elsewhere in the
protocol (platform fees, see §5), not minted from nothing, and 10% is
retained rather than paid out every block.

## 2. Bridge

**Current real status - including the open Critical finding.**

- **Confirmation depth**: a vault release only proceeds once
  `currentChainHeight() - BurnBlockHeight >= core.VaultReleaseConfirmationDepthBlocks`
  (= 8 CreditChain blocks) past the burn that opened it
  (`core/vault_release_approval.go`).
- **Validator signer model**: an M-of-N federated set of bridge validators,
  each independently holding their own external signing key (deliberately
  never custodied by this server - see `audit/KNOWN_GAPS.md`'s "Deliberately
  not attempted for the bridge validator key" entry). The vault gas-price
  oracle key has an optional AWS KMS-backed signer
  (`api/vault_gas_price_oracle_signer.go`); the bridge validator keys
  themselves do not, by design.
- **🔴 CRITICAL, STILL OPEN as of this writing**: the deployed mainnet vault
  contract's on-chain `threshold()` is **1**, matching the backend's own
  `BRIDGE_VALIDATOR_THRESHOLD=1`. With two validators registered, either one
  alone can sign a `release()` call moving the vault's entire real ETH/USDC
  balance, or attest a mint of unbacked ETH_CCC1/USDC_CCC1/BTC_CCC1, with
  zero cooperation from the other. See `audit/KNOWN_GAPS.md`'s "CRITICAL,
  STILL OPEN" entry (dated 2026-08-19/20) for the full writeup, on-chain
  verification detail, and the trust-minimized light-client direction chosen
  but not yet built. **This document does not resolve that finding** - it
  states it plainly so it can't be missed by only reading this spec.
- **Emergency pause lever (new, this revision)**: a general-electorate
  `EMERGENCY` governance proposal (see §6) can set the `BridgePaused`
  governance param to 1, which `applyBridgeDeposit`/`applyBridgeBtcDeposit`
  (`api/apply.go`) and `applyVaultBurn` (`api/vault_lock.go`) all check and
  refuse while set - a fast, still-decentralized way to halt new bridge
  deposits and new release requests if the threshold=1 exposure above is
  ever actually exploited, without relying on the admin multisig. This is a
  reactive circuit breaker, not a fix for the underlying threshold - the
  underlying fix is the separately-tracked light-client work above.

## 3. Treasury

- **Foundation Treasury** (`core/treasury.go`,
  `treasuryStore.Treasury.FoundationAddress`) is a real, operator-controlled
  address - not a synthetic pool address like the genesis pools below.
  Admin actions against it go through the on-chain M-of-N admin multisig
  (`core/admin_multisig.go`), not a single key, as of the 2026-07-20 fix
  documented in `audit/KNOWN_GAPS.md`.
- **Genesis pools** (`api/tokenomics.go`), a one-time mint locked 2026-07,
  1,000,000,000 CCT total - a slice of the overall supply cap (§5), not the
  whole thing, deliberately leaving headroom for the block-reward schedule:

  | Pool | Amount (CCT) | Share |
  |---|---|---|
  | Validator Reward | 400,000,000 | 40% |
  | Foundation Treasury | 250,000,000 | 25% |
  | Ecosystem Dev | 150,000,000 | 15% |
  | Governance Treasury | 100,000,000 | 10% |
  | Community Incentives | 100,000,000 | 10% |

- **`CCTBurnedPendingReward`** (`core/treasury.go`) is the running total of
  CCT genuinely burned (from platform/ad fee splits, see §5) since the last
  block reward paid out - the actual funding source for §1's block reward,
  reset to 0 each time `applyBlockReward` pays out.

## 4. Validator

- **Registration fee**: `ValidatorRegisterFeeCCT` = 50 CCT
  (`api/validators.go`), governance-overridable via the `PARAM_CHANGE`
  mechanism (§6) like every other governance-settable fee.
- **Delegation model**: `core.ValidatorDelegationStore`
  (`core/validator_delegation.go`) tracks real on-chain delegations per
  (validator, delegator) pair - a delegator's annual reward is
  `annualPoolReward * (10000 - CommissionBps) / 10000` (`api/validators.go`),
  i.e. the validator's own declared commission rate is deducted before the
  delegator's pro-rata share.
- **Commission**: `CommissionBps` is validator-set, capped at
  `core.MaxCommissionBps` = 5000 (50%) - enforced both at submission and
  again at apply time, never trusting the submission-time check alone.
- **Ban/rotation**: see §1's slashing table - a validator crossing
  `PermanentBanSlashCountThreshold` is permanently excluded from both
  registration and selection, enforced in `applyValidatorRegister` and the
  consensus loop's `hydrateValidators`.
- **Moderators** are a related but separate role
  (`api/moderator.go`/`website/moderation.html`): require reputation ≥ 50
  (the same floor as becoming a validator), ≥ 200 CCT already staked
  (locked as a moderation bond on registration), and a flat 10 CCT
  registration fee - also governance-overridable.

## 5. Tokenomics

- **Supply cap**: `MaxTotalCCTSupplyWhole` = 10,000,000,000 CCT
  (`api/tokenomics.go`). All minting (block rewards, genesis pools, any
  other mint path) goes through `mintCCTCapped`, which refuses to mint past
  this cap - current real headroom is this cap minus the 1B genesis mint
  minus whatever block rewards have paid out since.
- **REPT/RC**: "RC" is this protocol's user-facing name for REPT, the
  reputation-adjacent token that gates governance actions (e.g.
  `TokenCreationMinRC`, moderator registration's RC-ish stake language) and
  is earned through genuine participation (passing a governance proposal,
  moderator confirmations, etc.) - never freely transferable the way CCT is,
  by design.
- **Platform fee split** (`splitPlatformFee`, `api/apply.go`) - the
  established pattern every CCT-denominated protocol fee follows:

  | Destination | Share |
  |---|---|
  | Burned (feeds `CCTBurnedPendingReward`, §3) | 50% |
  | Validator pool | 20% |
  | Foundation Treasury | 10% |
  | Outernet operator | 10% |
  | Moderator (remainder, covers rounding) | 10% |

- **Sponsored-post ad fee split** (`api/ad_fee_split.go`) - a separate,
  governance-adjustable split (via the same `PARAM_CHANGE` mechanism, §6)
  reusing the platform fee's spirit but its own five keys/defaults:

  | Governance param key | Default |
  |---|---|
  | `AdFeeBurnBpsKey` | 4000 (40%) |
  | `AdFeeValidatorBpsKey` | 3000 (30%) |
  | `AdFeeTreasuryBpsKey` | 1500 (15%) |
  | `AdFeeModeratorBpsKey` | 1000 (10%) |
  | `AdFeeGovFundBpsKey` | 500 (5%) |

## 6. Governance

Every proposal kind (`core.ProposalKind`, `core/governance.go`), its quorum,
voting window, and - new in this revision - its timelock before a PASSED
outcome's real effect actually lands:

| Kind | Quorum | Voting window | Timelock | Effect on PASSED |
|---|---|---|---|---|
| Plain (`""`) | 500 (default) | 7 days | none (immediate) | None beyond REPT/Trust payouts - informational only |
| `PARAM_CHANGE` | 500 | 7 days | **48h** (new) | Writes `ParamKey`/`ParamValue` into `governanceParamsStore` |
| `VERSION_ACTIVATION` | 500 | 7 days | **48h** (new) | Sets `core.ActivatedProtocolVersion` |
| `TOKEN_CREATION` | 500 | 7 days | **48h** (new) | Registers the new token via `applyTokenCreationFromProposal` |
| `PREDICTION` | 500 | variable (1-60 days, proposal-chosen) | none | RC-bet pool resolution (§ separate from the Yes/No headcount quorum) |
| `MODERATOR_DISPUTE` | 500 | 7 days | none (immediate) | Reputation penalty against the named moderator |
| `NODE_ADMISSION` | 1,000 (doubled) | 7 days | none (immediate) | Grants peer-gossip admission to the named node identity |
| `EMERGENCY` (new) | 1,000 (doubled) | **24h** | **4h** | Writes the one allowlisted `BridgePaused` param (§2) |

**Why a timelock, and why these two exceptions stay immediate**: before this
revision, a `PARAM_CHANGE`/`VERSION_ACTIVATION`/`TOKEN_CREATION` proposal's
real effect landed in the very same block that finalized the vote - no
window for anyone to react (withdraw, adjust position) before a passed
change took effect. `MODERATOR_DISPUTE` and `NODE_ADMISSION` still execute
immediately: a reputation penalty against an already-identified bad actor
and a one-time peer-admission grant don't carry the same "protocol
parameters just changed under you" risk a `PARAM_CHANGE` does.

**Emergency path, honestly characterized**: `EMERGENCY` is a
real alternative decentralized path - open to the same general electorate
as every other kind (same `votingWeight` formula, no executive-committee
restriction), just faster (24h vote, 4h timelock) and harder to pass
(doubled quorum). It is deliberately scoped to a single allowlisted action
right now (pausing bridge deposits/releases, §2) rather than being a
general-purpose fast `PARAM_CHANGE`. This is distinct from, and should never
be confused with, the **admin multisig** (`core/admin_multisig.go`) - a
real M-of-N multisig (no single key can act alone, per the 2026-07-20 fix),
but still a small, closed committee, not the general electorate. The admin
multisig remains the fast path for the 9 admin-only actions it covers (mint,
treasury controls, genesis pool allocation, etc.) - it is correctly
described as a centralized-but-no-longer-single-key fast path, not a
decentralized emergency mechanism, and this document does not claim
otherwise.

---

## Open items

This document states the rules as they exist; it does not resolve every
known gap. See `audit/KNOWN_GAPS.md` for the full, continuously-updated list
- most relevantly for this spec:
- The bridge vault's on-chain signer threshold=1 Critical finding (§2) -
  still open at the time of writing.
- Every dollar/numeric value in this document reflects code at the time of
  writing; governance-adjustable values (marked above) can change by a
  future passed proposal - re-read `governanceParamsStore`'s live state
  (or the relevant source constant) rather than trusting this document's
  numbers as permanently fixed.

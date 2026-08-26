# CreditChain Economic Constants — Mainnet Recommendations

**Status:** Research/recommendation only. Nothing in this document has been implemented — every
number below is a proposal for the platform owner to review, adjust, and explicitly approve
before any code change lands. Where a number genuinely cannot be derived without a business
decision only the owner can make (target token price, target market cap, target APY), this
document says so explicitly and gives the formula/range that *would* determine it rather than
inventing a figure.

**Scope of this pass:** `core/staking_logic.go`, `core/consensus.go`, `api/validators.go`,
`api/tokenomics.go`, `core/slashing.go`, `core/dormancy.go`, `core/bridge_validator.go`,
`contracts/CreditChainVault.sol`, `api/consensus_loop.go`, and the "Economic / Game-Theoretic"
section of `audit/KNOWN_GAPS.md`. All values below were re-read from current source, not from
memory of earlier sessions — several numbers in scattered doc comments elsewhere in the repo are
stale (e.g. an old `5,000,000,000` supply cap reference in `KNOWN_GAPS.md`'s Governance section;
the real, current cap in `api/tokenomics.go` is `10,000,000,000`). This report cites the live
source values.

---

## 1. Unbonding / cooldown period for unstaking

### Current state

`core/staking_logic.go`:

```go
const UnbondingPeriodSeconds = 60
```

One constant, governance-adjustable at runtime (`api/admin_params.go`'s `adminSettableParams`),
and it is **shared** by two structurally different flows:

- `api/apply.go:3708` — a validator unstaking their *own* stake (`StakingStore`).
- `api/apply.go:4333` — a delegator undelegating stake they've *delegated to* a validator
  (`DelegationStore`).

These two flows are not equally risky. I traced `slashStake` (`api/apply.go:3920`) — it reads and
burns only from `stakingStore.Stakes[address]["CCT"]`, the validator's own direct stake ledger. A
delegator's position in `DelegationStore` is never touched by any slashing path in the codebase. So
CreditChain already has a real economic distinction between "validator self-stake (slashable)" and
"delegated stake (not slashable, but still counts toward the validator's effective weight via
`hydrateValidator`'s `wallet.StakedCCT + delegationStore.TotalDelegatedTo(...)`, `api/consensus_loop.go:66`)"
— it just doesn't have two separate unbonding constants for the two cases yet. That distinction
matters for the recommendation below.

### What comparable networks currently use (verified against 2026 data, not stale training-era numbers)

| Network | Validator/self-bond unbonding | Delegator/nominator unbonding | Notes |
|---|---|---|---|
| Cosmos Hub | 21 days (fixed, non-negotiable) | 21 days (same pool, delegators *are* slashed proportionally) | No distinction between validator and delegator risk — both share the 21-day figure because both share slash exposure. |
| Ethereum | Variable exit-queue based on churn limit (~256 ETH/epoch); typically 1–9+ days depending on how many validators are exiting concurrently | N/A — no separate delegation layer at protocol level | Exit itself doesn't remove slash exposure until finalized; congestion (record 9-day exits reported mid-2026) is a real, current operational risk, not just a design parameter. |
| Solana | ~1 epoch (2–3 days), ~2–5 days in practice depending on when in the epoch deactivation is requested | Same — no separate class | Solana has essentially no protocol-level slashing for most misbehavior, which is why it can run such a short cooldown safely. |
| Polkadot | **As of Referendum 1910 (enacted July 2026): validators retain a real, still-substantial self-bond (a mandatory 10,000 DOT minimum self-stake per Referendum 1909) and continue to carry direct slashing risk.** | Cut from 28 days to ~2 days — **specifically because this same referendum simultaneously removed nominators' slashing exposure entirely.** | This is the most directly relevant precedent: Polkadot didn't just "shorten unbonding," it shortened unbonding *for the class of staker that no longer bears slashing risk*, while keeping the slashable validator-self-bond model intact. |

The 2026 Polkadot change is worth calling out specifically because it would be easy to
misread as "the industry is moving toward ~2-day unbonding" and copy that number. It is not a
general trend toward shorter *slashable* bonding — it's a trend toward decoupling *slashable*
capital (kept long-bonded) from *non-slashable delegated* capital (shortened, since there's
nothing left to protect against once slashing risk is removed). CreditChain's own
`DelegationStore`/`slashStake` split already puts it on the Polkadot side of that line, not the
old Cosmos side.

### CreditChain-specific risk factors that argue for a real, non-trivial unbonding period

This isn't a generic PoS chain — `UnbondingPeriodSeconds` is protecting a validator set that sits
adjacent to a real multisig vault bridging live ETH/BTC/USDC value (`contracts/CreditChainVault.sol`).
A few things specific to this codebase push toward the longer end of the comparable-network range,
not the shorter end currently in vogue:

1. **Bridge-validator and PoSR-validator identity overlap is an operator choice the code doesn't
   prevent.** `core/bridge_validator.go`'s own doc comment: *"Same humans can staff both
   registries — that's an operator choice, not something this code enforces."* The two credential
   sets are cryptographically distinct (Ed25519 CreditChain key vs. secp256k1 vault-signing key),
   but nothing stops the same operator from holding both. A validator whose CCT stake exits too
   quickly after misbehavior is detected also gives that same operator's bridge-adjacent trust less
   time to be reviewed.
2. **Missed-block slashing requires M-of-N corroboration, which takes real wall-clock time to
   accumulate**, not instant single-node detection (`api/missed_block_confirmation.go`,
   `requiredMissedBlockConfirmations = N/2 + 1`, resolved 2026-07-20 per `KNOWN_GAPS.md`). A
   too-short unbonding window can race this corroboration process: if a validator can queue an
   unstake and have it mature before a strict majority of independently-operated nodes finishes
   corroborating a missed-block pattern, the network loses its own designed safety margin.
3. **No state rollback on reorg** (`KNOWN_GAPS.md`, "Consensus / Validator Security" — a real,
   live-tested gap: two genuine node1/node2 forks required manual operator intervention during this
   project's own testnet phase) and **fallback block production is explicitly "a deadlock-breaker,
   not proper leader election"** — both self-disclosed as not fully BFT-robust. On a chain with
   this profile, evidence of misbehavior is more likely to surface via slower, partly-manual
   forensic review than on a mature chain with automated rollback — which is the opposite condition
   Polkadot's 2026 change relied on to justify going short.

### Recommendation

| Constant | Current | Recommended | Basis |
|---|---|---|---|
| `UnbondingPeriodSeconds` (validator self-stake — the higher-risk, slashable case this constant currently governs for *both* flows) | 60 (demo) | **1,814,400 seconds (21 days)** | Matches Cosmos Hub's long-standing validator-bond figure and sits close to Polkadot's *pre-2026* validator-adjacent standard; gives realistic headroom for M-of-N missed-block corroboration, forensic review without automated rollback, and any bridge-adjacent trust review, without being needlessly punitive (a full quarter, à la old 28-day Polkadot, is not obviously better and mainly just locks up more honest capital for the same protection). |

**Recommended follow-up (a code change, not a constants tweak — flagging for the backlog, not
required to ship a first mainnet number):** split `UnbondingPeriodSeconds` into two
governance-tunable parameters — one read at `api/apply.go:3708` (validator self-stake) and one at
`api/apply.go:4333` (delegator stake) — mirroring the real risk split that `slashStake` already
enforces. Once split, the delegator-only figure could reasonably follow the shorter,
non-slashable-capital convention (e.g. 3–7 days, in the Solana/new-Polkadot-nominator range) since
delegated CCT is never seized by any code path today. Until that split exists, the single shared
constant should be sized for the *worse* of the two cases (the validator's own slashable stake),
which is exactly what the 21-day recommendation above does — it's a safe default for delegators
too, just more conservative than strictly necessary for them.

**`DormancyPeriodSeconds` — same file, same category, but not actually a research question.**
`core/dormancy.go`'s own comment already states the intended real value: *"300, demo-scale stand-in
for 50 years."* This isn't a judgment call needing a new derivation — it's a placeholder for a
number the design already settled on. Recommended value: **1,577,880,000 seconds** (50 years ×
365.25 days, accounting for leap years). Flagging it here only because it sits in the same
`core/staking_logic.go`-adjacent, demo-scale-constant category `KNOWN_GAPS.md` groups it with.

---

## 2. Marketplace / fee-related economic constants

### Constants directly named in the request

| Constant | File | Current | Category | Recommendation |
|---|---|---|---|---|
| `MinValidatorStake` | `core/consensus.go` | 1,000 CCT | **Objectively under-tuned relative to CreditChain's own selection formula** — see analysis below | **10,000 CCT** (10× current) |
| `ValidatorRegisterFeeCCT` | `api/validators.go` | 50 CCT | Internally-consistent-ratio, not comparable-network-derived (no real network has a direct analog to a flat one-time anti-spam fee separate from stake) | **500 CCT** — keep proportional to the recommended `MinValidatorStake` (~5%), so it stays a secondary spam deterrent, not a second security mechanism |
| `validatorScalingBlockSize` / `validatorScalingIncrementPercent` | `api/validators.go` | 10 validators / +10% per block | Mechanism shape is sound; the specific pair is arbitrary without a target validator-set size | **Needs owner input** — see below |

### `MinValidatorStake`: why this one has a real, price-independent answer

The user's framing for this section specifically calls out "minimum stake sizing relative to total
supply, to avoid trivial Sybil validator registration" as the kind of constant that *does* have an
objectively reasonable range without needing a token-price assumption. Tracing CreditChain's own
selection math confirms this is exactly the right lens here, and reveals a concrete problem:

- `StakeCap = 100_000` (`core/consensus.go`) is the stake level at which the 25% stake component
  maxes out. At the current `MinValidatorStake = 1,000`, a freshly-registered validator's
  normalized stake is `1,000 / 100,000 = 1%`, contributing only `0.25%` to total selection weight.
- But `NewWallet()` (`core/wallet.go:138`) sets every new address's `Reputation` to a flat **50**
  out of `MaxReputation = 100` — already above `MinValidatorReputationGate = 20`, so a brand-new
  Sybil identity clears the reputation gate on day one, contributing `0.5 × 55% = 27.5%`.
- `ValidatorStatus.Uptime()` (`core/validator_store.go:66`) defaults to a clean **1.0** before a
  validator has produced or missed a single block, contributing the full `20%`.
- Sum for a brand-new, minimum-stake validator: `0.25% + 27.5% + 20% ≈ 47.75%` of the maximum
  possible selection weight — for roughly 1,050 CCT (current stake + fee), with essentially no
  track record.

This means the "reputation dominates wealth by design" property the 55/25/20 split is meant to
express doesn't actually differentiate a new entrant from an established validator *at
registration time* — every new validator starts at the same neutral reputation and perfect uptime.
The only real lever standing between "cheap to Sybil" and "meaningfully capital-gated" at
registration is the stake component, and it's currently set to barely move the needle (1% of the
cap that makes it matter). An attacker registering, say, 10 validators at ~1,050 CCT each
(~10,500 CCT total, before the scaling step-up) could acquire roughly 10× that ~48% relative
weight in aggregate block-production probability — a capital-inefficient way to gain influence
compared to concentrating the same CCT into one large-stake validator, which is exactly backwards
from what stake-weighted consensus is supposed to incentivize.

Raising `MinValidatorStake` to 10,000 CCT (10% of `StakeCap`) changes the stake contribution for a
floor-stake validator from `0.25%` to `2.5%` of total weight — still small relative to reputation
(intentionally; that's the whole "not Proof of Richest" point), but large enough that fielding N
cheap validators now costs proportionally more relative to the influence gained, and the fee/stake
scaling (`validatorScalingIncrementPercent`) compounds that cost further as N grows. This is a
formula-driven, price-independent recommendation: it's about the *ratio* between
`MinValidatorStake` and `StakeCap`, not about what either is worth in dollars.

**What genuinely needs the owner's input here:** the *absolute* USD-denominated cost of mounting a
Sybil attack (is 10,000 CCT worth $500 of attack-resistance or $500,000?) depends entirely on
CreditChain's target token price, which this report cannot and should not guess. Formula for the
owner to apply once a price target exists:

```
Total cost to field N Sybil validators (USD) ≈
    N × (MinValidatorStake + registrationFeeForCount(N)) × target_CCT_price_USD
```

For reference, real networks' *effective* minimum validator cost (not always a literal "minimum
stake" field, but the practical floor to run a validator that matters) tends to land somewhere in
the tens-of-thousands to low-hundreds-of-thousands of USD range (Ethereum's 32 ETH is priced in
the low-to-mid five figures depending on ETH price; Cosmos/Polkadot's effective floors to crack the
active set are usually higher). If the owner has a target CCT price in mind, back-solving
`MinValidatorStake` from a target USD floor in that range (using the formula above) is a
reasonable way to set the *absolute* number — the 10,000 CCT / 10%-of-`StakeCap` figure above is
the price-independent floor; the owner may want to go higher once a price target exists.

### `validatorScalingBlockSize` / `validatorScalingIncrementPercent`: mechanism is fine, the numbers need a target

The stepped-cost-escalation *shape* (flat for the first N validators, then +X% per additional
block of N) is a reasonable anti-unbounded-growth mechanism and isn't obviously wrong at 10/10% —
but it isn't derived from anything either; its own doc comment calls it "a judgment call (not
derived from any specified curve)." The real input this needs is a **target active validator-set
size**, which is a business/decentralization-philosophy decision, not something computable from
current constants:

| Network | Approximate active validator/nominator count | Philosophy |
|---|---|---|
| Cosmos Hub | ~180 (hard-capped active set) | Curated, small, high-stake-per-validator |
| Polkadot | ~1,000+ (validators + nominator pool combined) | Larger but still curated via nomination |
| Solana | 1,000+ | Low per-validator floor, permissionless-leaning |
| Ethereum | 1,000,000+ | Maximally permissionless (32 ETH floor is low relative to total value secured) |

**This is a "needs owner input" item, not a copyable number** — CreditChain's answer depends on
whether the owner wants a small, curated, Cosmos-style validator set (favors raising
`MinValidatorStake` further and/or making the scaling step steeper) or a larger, more
permissionless one (favors keeping `MinValidatorStake` closer to the price-independent floor above
and leaving the scaling curve gentle). Once a target count exists, `validatorScalingBlockSize`/
`validatorScalingIncrementPercent` can be tuned so the cost curve reaches "prohibitively
expensive" right around that target size, rather than being an arbitrary flat 10/10%.

### Other Economic/Game-Theoretic constants flagged in `audit/KNOWN_GAPS.md` (swept per request)

These were checked against the same "objectively-tunable vs. needs-owner-input" test. Most are
already self-documented in code as deliberate, reasonable-starting-point judgment calls the team
already planned to revisit with real usage data — listed here for completeness, not because
they're blocking:

| Constant | File | Current | Assessment |
|---|---|---|---|
| `MaxTestStakePerAccount` | `core/staking_logic.go` | 10,000 | **Not a tunable number at all — its own doc comment says it's "a shared-pilot fairness cap, not an economic security mechanism" because "there's no real value at stake yet."** That justification is gone once real value is at stake. Recommend removing/relaxing this cap for mainnet rather than picking a new number for it — if the owner wants a real per-account holding limit for AML/compliance reasons, that's a legal/compliance decision (jurisdiction-dependent), not an economic-model derivation. Needs owner + compliance input, not a copyable figure. |
| 1 CCT flat `TxExchange` transfer fee | `core/token_transactions.go` | 1 CCT (rounded up from an originally-requested 0.5 CCT, architecturally unrepresentable under the current `uint64`-only, no-sub-unit-precision balance model) | **Needs owner input on target USD fee** (is 1 CCT meant to cost the user ~$0.01 or ~$10? entirely a function of target token price) **plus a separate engineering decision** (a milli-CCT precision migration would allow exact fractional fees instead of forcing round-number workarounds — worth doing before mainnet if fee precision matters economically, per `KNOWN_GAPS.md`'s own note). |
| `MissedBlockStakeSlashBps` (0.50%) / `DoubleSignStakeSlashBps` (10.00%) | `core/slashing.go` | Locked 2026-07 | Sized proportionally to existing reputation penalties (2 vs. 50 points), not independently derived — reasonable relative shape (double-sign is 20× more severe than a missed block, matching that it attacks consensus itself), no comparable-network number to benchmark against directly since slash percentages vary widely by chain (Cosmos ~5% double-sign, Ethereum's correlation-penalty model is entirely different in shape). Not flagged as urgent — internally consistent, already "locked." |
| `PermanentBanSlashCountThreshold` (6) | `core/slashing.go` | Locked 2026-07 | Same category as above — judgment call, internally consistent with the strike-weight system, not urgent. |
| `DefaultProposalQuorum` (500) / `ProposalVotingWindowSeconds` (7 days) / governance `votingWeight` 60% reputation / 40% stake split | `api/governance_weight.go`, `core/governance_params.go` | — | Self-documented as "judgment calls, not derived from a specified model," calibrated for current small network scale. Worth revisiting with real participation data once mainnet is live — not a pre-launch blocker. |
| Borrowing Power Score weights (reputation 40% / repayment 25% / staked CCT 15% / collateral 10% / governance 10%) | `api/borrowing_power.go` | — | Same "reasonable starting formula, not derived from a specified economic model" category, self-documented as such. Lending-marketplace-adjacent but not a validator/staking economic-security parameter — lower priority for this pass. |

`TPS`/`GasPrice` in `api/network.go` are excluded from this table entirely — they're hardcoded
cosmetic stub values with explicit `// TODO` comments and nothing downstream depends on them being
real; not an economic constant in any sense that needs tuning.

---

## 3. PoSR reward weights and block-reward emission schedule

> **SUPERSEDED (2026-08-25).** The "what genuinely needs the owner's business input here" question
> below has been answered — explicitly, by the operator — but with a design not among the three
> options this section listed. Rather than continuing the relative-decay curve, switching to
> Cosmos-style bonded-ratio issuance, or keeping the flat 3 CCT/block floor, the block reward is now
> tied directly to real fee-burn economic activity: 90% of whatever CCT was burned from marketplace
> platform fees since the last block (`core.Treasury.CCTBurnedPendingReward`,
> `api/apply.go`'s `pendingBurnBasedBlockReward`), reset to 0 every payout. `blockRewardSchedule`,
> `blockRewardEra`, `secondsPerYear`, `blockRewardForTimestamp`, and `ProduceBlockReward` have all
> been removed from `api/tokenomics.go`/`api/validators.go`. This sidesteps the "flat forever"
> concern raised below entirely — there is no wall-clock schedule left to hold flat or decay; a
> block with zero fee/burn activity since the last one now correctly pays zero reward, and the
> per-block amount scales automatically with real platform usage rather than a hardcoded curve.
> The analysis below is left intact as the historical record of what was true before this date.

### Current values (re-verified against source, not assumed from prior session notes)

`core/consensus.go`:

```go
posrStakeWeightFixed      = 250_000 // 0.25 * posrPrecision   →  Stake:      25%
posrReputationWeightFixed = 550_000 // 0.55 * posrPrecision   →  Reputation: 55%
posrUptimeWeightFixed     = 200_000 // 0.20 * posrPrecision   →  Uptime:     20%
```

Confirmed current — matches the prior session's 55/25/20 figures, and the doc comment
confirms these were "Approved 2026-07," locked. The three weights sum to exactly 100%
(`250,000 + 550,000 + 200,000 = 1,000,000 = posrPrecision`) — arithmetically consistent, and the
fixed-point integer implementation (deliberately avoiding `math.Pow` for cross-platform
determinism) is sound engineering, not a placeholder.

`api/tokenomics.go`'s `blockRewardSchedule`:

| Era | Start year | Reward/block | Relative to Year 1 |
|---|---|---|---|
| 1 | 0 | 10 CCT | 1.00x |
| 2 | 1 | 8 CCT | 0.80x |
| 3 | 2 | 6 CCT | 0.60x |
| 4 | 3 | 5 CCT | 0.45x |
| 5+ | 4 | 3 CCT | 0.30x — **held flat forever after this** |

Block cadence is `consensusTickInterval = 8s` (`api/consensus_loop.go:11`), giving
`3,942,000` blocks/year (`31,536,000s ÷ 8s`) — this matches `api/reputation_mining.go`'s
independently-stated "10,800 blocks, ~1 day" figure exactly (`86,400 ÷ 8 = 10,800`), a useful
cross-check that the two files agree on cadence.

### Assessment: are the weights and the emission curve internally consistent?

**The 55/25/20 split is philosophically coherent and already approved — not what needs
attention.** What the trace in Section 2 surfaces, though, is that the split's *safety property*
("reputation dominates wealth by design, not Proof of Richest") depends on `MinValidatorStake`
actually constraining a floor-stake validator's weight meaningfully — and today it doesn't
(Section 2's ~48%-of-max-weight-for-1,050-CCT finding). This isn't a flaw in the weights
themselves; it's a cross-constant dependency the weights create that `MinValidatorStake` currently
fails to satisfy. Recommend reading Section 2's `MinValidatorStake` recommendation as part of this
finding, not a separate issue — the two constants are coupled by the selection formula whether or
not the code treats them as related.

**The block-reward schedule's first four years are a reasonable, deliberately-reasoned decay
curve** — `KNOWN_GAPS.md` documents exactly why the *relative* shape (1.00x/0.80x/0.60x/0.45x/0.30x)
was preserved from a fractional-CCT spec onto whole-unit CCT amounts, which is a legitimate,
disclosed translation, not an arbitrary placeholder.

**"Held flat thereafter" past Year 5, however, reads as an implementation default filling a gap
the spec left open, not a deliberately-reasoned terminal-emission choice** — `KNOWN_GAPS.md` says
plainly "the spec only defines 5 years," and nothing in the code comments claims a considered
reason for holding flat forever specifically (contrast with the 55/25/20 weights and the slash bps,
both of which do carry "approved"/reasoning language). Working out the actual scale of what "flat
forever" means: at 3 CCT/block × 3,942,000 blocks/year ≈ **11,826,000 CCT/year, forever**, against
a `MaxTotalCCTSupplyWhole = 10,000,000,000` cap (`api/tokenomics.go:22`) with 1,000,000,000
earmarked for one-time genesis pools — years 1–4 alone mint ≈114,318,000 CCT from block rewards,
leaving roughly 8.9 billion CCT of headroom, which the flat 3 CCT/block rate would take on the
order of **750+ years** to exhaust. In practical planning-horizon terms, that's equivalent to
"CCT inflates forever at a fixed nominal rate," with the *percentage* inflation rate shrinking
asymptotically toward zero as circulating supply grows — a shape closer to a permanently-diluting
fixed-nominal-issuance model than to either Bitcoin's discipline (halves toward literal zero by a
fixed date) or Cosmos's dynamic bonded-ratio targeting (issuance adjusts to hit a target % of
supply staked, rather than following a wall-clock schedule at all).

### What genuinely needs the owner's business input here

This is not a number this report can responsibly invent — it's a real design-philosophy decision
with no universal template, and the three live comparable models pull in different directions:

1. **Continue the relative-decay curve indefinitely** (Bitcoin-style: another step down every N
   years, approaching but never quite reaching zero). Needs the owner to pick the additional
   era length and decay rate — there's no formula that derives this without a target terminal
   inflation rate and a target timeline in mind.
2. **Switch to Cosmos-style dynamic issuance**, where the block reward is a function of what
   fraction of circulating CCT is actually bonded (staked), targeting a specific bonded-ratio band
   (Cosmos targets ~67% bonded, with issuance ranging ~7–20% APY to pull toward that target).
   Needs the owner to pick: a target bonded ratio, and min/max issuance bounds. This is a larger
   code change than a constants tweak (the schedule would need live stake-ratio input, not just a
   timestamp), so it's worth deciding early if it's the direction the owner wants.
3. **Deliberately keep the current flat-3-CCT-forever floor** — this is a legitimate choice (a
   permanently low, non-zero terminal inflation rate is a defensible design), but it should become
   an explicit, documented decision rather than the accidental result of "the spec only defined 5
   years." Cheapest option: zero code change, just needs the owner to say "yes, that's what we
   want" so it stops reading as an unfinished placeholder to a future auditor.

**Formula the owner needs for option 2, if that direction is chosen** (for reference, not a
recommendation to adopt it):

```
block_reward = base_reward × f(actual_bonded_ratio, target_bonded_ratio)
```

where `f` pulls issuance up when actual bonding is below target (incentivize more staking) and
down when above target (avoid over-diluting). This requires the owner to set `target_bonded_ratio`
and the issuance band — genuinely not derivable from CreditChain's current constants, since nothing
in the codebase today tracks or targets a bonded-ratio at all.

**No specific number is recommended for what happens after Year 5** — this document flags the gap
and the three options precisely so the owner can pick one deliberately, per the request not to
invent a figure for something that's really a business decision.

---

## Summary table — everything with a concrete recommended number

| Constant | File | Current | Recommended | Type |
|---|---|---|---|---|
| `UnbondingPeriodSeconds` | `core/staking_logic.go` | 60s | **1,814,400s (21 days)** | Comparable-network-derived |
| `DormancyPeriodSeconds` | `core/dormancy.go` | 300s | **1,577,880,000s (50 years)** | Already specified by the code's own comment — not a new derivation |
| `MinValidatorStake` | `core/consensus.go` | 1,000 CCT | **10,000 CCT** (10% of `StakeCap`) | Price-independent, formula-derived from CreditChain's own selection weighting |
| `ValidatorRegisterFeeCCT` | `api/validators.go` | 50 CCT | **500 CCT** (keep ~5% of `MinValidatorStake`) | Internal-ratio-derived |
| `MaxTestStakePerAccount` | `core/staking_logic.go` | 10,000 | **Remove/relax**, replace only if compliance needs a real cap | Needs owner + compliance input, not an economic derivation |

## Summary table — everything genuinely needing the owner's own business input (no figure invented)

| Item | What's needed from the owner | Formula/range once available |
|---|---|---|
| Absolute USD-denominated Sybil-attack cost floor for `MinValidatorStake` | Target CCT price | `N × (MinValidatorStake + fee) × price_USD` — see Section 2 |
| `validatorScalingBlockSize` / `validatorScalingIncrementPercent` final tuning | Target active validator-set size (curated ~180-1,000 vs. permissionless 1,000+) | Tune step size so cost curve reaches "prohibitive" near the target count |
| `MaxTestStakePerAccount` replacement (if any) | Compliance/AML holding-limit requirements, if any | Jurisdiction-dependent, not economic-model-derived |
| 1 CCT flat transfer fee's real target | Target CCT price (is 1 CCT meant to be ~$0.01 or ~$10?) | `target_fee_USD / target_CCT_price` — plus consider a milli-CCT precision migration |
| Block-reward emission past Year 5 | Which of the three models in Section 3 (continued decay / dynamic bonded-ratio targeting / deliberate flat floor) | See Section 3 for each model's required inputs |

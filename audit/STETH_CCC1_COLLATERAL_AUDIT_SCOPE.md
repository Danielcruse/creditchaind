# STETH_CCC1 Loan Collateral — Audit Instructions

Focused audit-scope addendum for one feature: STETH_CCC1 (real, vault-locked
Lido stETH) as loan collateral, built 2026-08-11. Read this alongside the main
package (`README.md`, `ARCHITECTURE.md`, `THREAT_MODEL.md`, `KNOWN_GAPS.md`,
`HARDENING_HISTORY.md` §11) rather than instead of it — this document narrows
to exactly what's new and what an auditor should specifically poke at.

**This is the first collateral type backed by a real, off-chain-valued
asset.** CCT and OETH collateral are pure in-memory counters with nothing
external behind them; a bug in their accounting is contained inside
CreditChain's own ledger. STETH_CCC1 collateral is real vault-locked stETH —
a bug here can strand or misdirect a real asset. Weight review time
accordingly.

## 1. Where to start reading

In dependency order, not file order:

1. `core/governance_params.go` — the new `SetAt`/`LastSetAt`/`IsStale`
   staleness-tracking mechanism, deliberately separate from the existing
   plain `Set`/`Get`.
2. `core/liquidation.go` — `LiquidationThresholdPercentFor`, the per-symbol
   override table.
3. `api/apply.go`:
   - `applyLockCollateral`'s `STETH_CCC1` case, `collateralEscrowAddress`,
     `creditCollateralTo`'s `STETH_CCC1` case — the real balance movement.
   - `collateralValueInYen` — pricing, staleness gate, depeg haircut, all in
     one function with a long doc comment explaining each.
   - `stethCcc1WeiNeededForYenValue` and its doc comment — the inverse
     conversion, and the exact magnitude-mismatch bug class it exists to
     prevent.
   - `reserveCollateralForLoan` — read the doc comment on `reservedAmount`
     carefully; this is where a real bug was caught and fixed (see §3
     below) and is the single highest-value function to re-derive
     independently rather than trust the doc comment.
   - `lenderCanReceiveStethCcc1`, and its two call sites in
     `applyLoanOfferDraw`/`applyLoanRequestFulfill`.
   - `meetsMinimumCollateralRatio` (used by `applyLoanRequestCreate`) — the
     loan-request-side twin of the same magnitude-mismatch fix.
4. `api/lock_collateral.go`, `api/loan_requests.go`, `api/collateral.go`,
   `api/loan_collateral.go`, `api/admin_params.go` — the HTTP-layer
   surface: request validation, the admin-settable-params allowlist
   (`StethEthRatioBasisPoints`), response shapes.
5. `website/js/borrow.js` / `website/borrow.html` — frontend wei/decimal
   conversion (`decimalToSmallest`/`smallestToDecimal` from `vault.js`,
   the same pattern `staking.js` already established for ETH_CCC1) and the
   live calculator's real-price estimate.

## 2. Specific things to independently verify, not just read

- **Re-derive `stethCcc1WeiNeededForYenValue`'s formula by hand** against
  `collateralValueInYen`'s STETH_CCC1 case and confirm they're true
  mathematical inverses of each other (including the `StethEthRatioBasisPoints`
  haircut applied on both sides — one directly, one via the `10_000/ratioBps`
  inversion). A one-sided haircut (applied to valuation but not to the
  reservation sizing, or vice versa) would silently under- or
  over-collateralize every loan reserved while a depeg override is active.
- **Confirm every real-balance transfer stays inside one wei-scale
  accounting domain.** `stethCcc1Store` (18-decimal, wei) is the only real
  ledger; `collateralStore.Locked` (a plain `uint64` map) tracks the SAME
  wei-scale numbers for bookkeeping, never a converted/scaled figure. Any
  new code path that mixes a `uint64` "whole count" convention (like
  CCT/OETH collateral) with STETH_CCC1's wei convention is a red flag —
  check any new PR touching `collateralStore` for this specifically.
- **Confirm `reserveCollateralForLoan`'s `reservedAmount` return value is
  used consistently.** Search for every call site
  (`grep -n "reserveCollateralForLoan(" api/*.go`) and confirm each one
  uses the returned `reservedAmount` — never the original loan amount —
  for both the resulting `Loan.CollateralAmount` field and any
  refund-on-failure path. This exact class of bug (using the wrong operand)
  was caught once already during this feature's own development (see
  `HARDENING_HISTORY.md` §11) — a new call site introduced later without
  this same care would reintroduce it.
- **Confirm the staleness window (`EthUsdCentsPerEthMaxAgeSeconds`, 24h) is
  actually enforced on every STETH_CCC1 pricing path**, not just
  `collateralValueInYen`'s direct callers. `stethCcc1WeiNeededForYenValue`
  has its own independent `IsStale` check — verify it wasn't skipped on any
  new call site.
- **Confirm the unregistered-lender gate can't be bypassed by a different
  funding path.** The two known ways a loan gains a real lender are
  `applyLoanOfferDraw` and `applyLoanRequestFulfill` — both gated. If a
  third loan-funding path is ever added (or already exists and was missed),
  it needs the same `lenderCanReceiveStethCcc1` check or the seizure gap
  reopens for that path.
- **Confirm `core.LiquidationThresholdPercentFor` is not reachable via any
  admin/governance transaction.** It's deliberately a hardcoded Go map, not
  a `governanceParamsStore` entry — grep for `LiquidationThresholdPercentFor`
  and `liquidationThresholdPercentBySymbol` to confirm nothing writes to it
  at runtime. This is a deliberate divergence from `EthUsdCentsPerEth`/
  `StethEthRatioBasisPoints` (which ARE governance-settable) — the
  distinction is intentional (see `core/liquidation.go`'s doc comment) and
  should stay that way.

## 3. Known, disclosed, NOT fixed in this pass

Full detail in `KNOWN_GAPS.md`'s "STETH_CCC1 as loan collateral" section.
Summarized here for quick audit triage:

| Gap | Severity | Why not fixed now |
|---|---|---|
| Seizure to an unregistered lender was closed for the two known funding paths, but a future third path could reopen it | Low (requires new code, not a standing gap) | Structural risk, not a live bug — flagged so future PRs check for it |
| uint64 wei ceiling (~18.4 stETH per single lock/reservation) | Low (matches existing ETH_CCC1 staking precedent) | Would require migrating collateral accounting to `DecimalTokenStore`/big.Int — a much larger refactor than this feature's scope |
| No multi-source oracle for `EthUsdCentsPerEth` — a single admin-set value, now with a 24h staleness gate but no live tick-by-tick feed | Medium | Same centralization tradeoff already accepted for `BtcUsdCentsPerBtc`/every other admin-tunable rate in this codebase; a real oracle integration is a separate, larger project |
| `StethEthRatioBasisPoints` requires a human to notice a depeg and update it — no automated feed | Medium | Same reasoning as above; the haircut mechanism exists, the automation doesn't |

## 4. Test coverage map

Run before and after any change touching this feature:

```bash
go build ./... && go vet ./... && go test ./...
```

Feature-specific tests, all in `api/steth_collateral_test.go` unless noted:

- Lock/release real-balance movement + insufficient-balance rejection
- `collateralValueInYen` STETH_CCC1 pricing, no-live-price fail-closed,
  staleness boundary (fresh vs. just-past-window), depeg haircut math
- `meetsMinimumCollateralRatio` magnitude-mismatch fix (dust wei vs. a real
  loan amount) + CCT/OETH-unchanged regression
- `hasEnoughCollateral`/`reserveCollateralForLoan` same magnitude-mismatch
  fix, plus the `reservedAmount` correctness fix
- Real-money seizure to the lender (`liquidateLoan`)
- Unregistered-lender funding refusal — both `applyLoanOfferDraw` and
  `applyLoanRequestFulfill`, both the refusal and the positive
  (registered-lender-succeeds) case
- Full `processCollateralLiquidations` sweep discovering and progressing a
  STETH_CCC1 loan through every risk stage, alongside a healthy loan left
  untouched in the same sweep pass
- `core/liquidation_test.go` — `LiquidationThresholdPercentFor`'s per-symbol
  lookup and default fallback
- `TestEndToEnd_StethCcc1LoanRequestCreateFulfillRepay` — full lifecycle,
  lock → request → fulfill → repay → collateral released

**Not yet covered** (candidates for the audit team or a follow-up session):
a full HTTP-layer integration test hitting `HandleLockCollateral`/
`HandleCreateLoanRequest` directly (current tests call the `apply*`
functions directly, matching most of this codebase's existing test style,
but skip the HTTP validation layer itself); a live browser click-through of
`borrow.html`'s new STETH_CCC1 UI (blocked in the environment this feature
was built in — verified instead via byte-for-byte deployed-file diff against
source and DOM/ID cross-checks against the JS that reads them, not a real
render).

## 5. Handoff notes for the app migration

Since the next step after this audit pass is migrating to a mobile/native
app client, worth carrying forward:

- **Every new STETH_CCC1 amount the client sends is wei-scale (18 decimals),
  never a "whole stETH" float.** The web frontend converts at the boundary
  via `decimalToSmallest`/`smallestToDecimal` (`website/js/vault.js`) using
  `BigInt`, never a JS `Number` — the app client needs the equivalent
  arbitrary-precision integer handling (whatever the app's language's
  BigInt/BigDecimal equivalent is), or amounts above ~0.009 stETH will
  silently lose precision (2^53 float safe-integer boundary, same
  well-known trap this codebase's own doc comments call out repeatedly for
  every other wei-scale value in the system: `ETHB` bridge withdrawals,
  `ETH_CCC1` staking, etc.).
- **New endpoints/fields to wire up**: `/collateral`'s response now
  includes `lockedSTETH_CCC1`; `/loans/health`'s response now includes
  `thresholdPercent`; `/collateral/lock`'s `symbol` field accepts
  `"STETH_CCC1"`; `/loan-requests/create`'s `collateralSymbol` accepts
  `"STETH_CCC1"`, and its `collateralAmount` field for that symbol is
  wei-scale (see above) — must be sent as a raw JSON integer literal
  (`apiPostSignedRaw`'s pattern), not round-tripped through a parsed
  number type that could lose precision.
- **`/admin/params` now also reports `StethEthRatioBasisPoints`** (if ever
  set) alongside the existing `EthUsdCentsPerEth` — relevant if the app
  ever surfaces a collateral-health/calculator screen, since an accurate
  estimate needs both values, not just the ETH/USD rate.
- **A STETH_CCC1-collateralized loan's real-money seizure path requires the
  lender to have registered a self-custodial ETH address before funding
  succeeds at all** (see §2 above) — if the app exposes a "fund this loan
  request/offer" action, a rejected STETH_CCC1 funding attempt should surface
  a clear, distinct error message (the API already returns one — see
  `api/loan_offers.go`/`api/negotiations.go`'s error strings) rather than a
  generic failure.

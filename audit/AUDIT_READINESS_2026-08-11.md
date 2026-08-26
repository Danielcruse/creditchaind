# CreditChain — Audit Readiness Summary

**Prepared:** 2026-08-11, ahead of mobile app migration
**Scope of this document:** everything built/changed in this session (STETH_CCC1 collateral, ETH vault release relay fix, bounded gas-price oracle), plus a pointer to the full historical disclosure list in `audit/KNOWN_GAPS.md`. This is a **handoff summary for an external auditor**, not a replacement for reading the code or `KNOWN_GAPS.md` directly.

---

## 1. What shipped this session

### 1.1 STETH_CCC1 as loan collateral
First collateral type backed by a **real balance** (vault-locked stETH) rather than a pure internal counter (unlike CCT/OETH).

- **Lock/release**: `api/apply.go`'s `applyLockCollateral`/`creditCollateralTo` move real balance into/out of a dedicated escrow address (`COLLATERAL:STETH_CCC1`).
- **Valuation**: `collateralValueInYen` prices it via the admin-governed `EthUsdCentsPerEth` rate, with a `StethEthRatioBasisPoints` haircut for stETH/ETH depeg risk (default 1:1, admin-adjustable), fails closed if the rate is stale (see §1.3) or unset.
- **Design decision (user-confirmed)**: 1 YEN-e unit == 1 USD cent, rather than routing through the CCT↔YENE AMM pool (deliberately avoids exposing a real-asset collateral type to a pool a large trade can move).
- **Fixed during build**: a real vulnerability where the self-funded/negotiation collateral-reservation path (`hasEnoughCollateral`, `reserveCollateralForLoan`, `peekCollateralSymbolForLoan`) compared raw wei amounts against whole-unit YEN-e loan amounts — off by up to 18 orders of magnitude, which would have made STETH_CCC1 collateral requirements nearly vacuous. Fixed via `stethCcc1WeiNeededForYenValue`, the priced inverse.
- **Proactive liquidation** (`processCollateralLiquidations`) now also sweeps STETH_CCC1 loans, not just OETH — deliberate, since real-asset price risk needs early-liquidation protection more than OETH's synthetic AMM price ever did.
- **Disclosed, not fixed** (see `audit/KNOWN_GAPS.md`, "STETH_CCC1 as loan collateral" section): a lender who has never registered a self-custodial ETH address could receive seized collateral at a technically-deterministic but hard-to-reach derived address. Low practical likelihood, not structurally prevented.
- **Test coverage**: `api/steth_collateral_test.go` — lock/release real-balance movement, insufficient-balance rejection, valuation math, the wei-vs-YEN-e magnitude fix, liquidation seizure to a real lender balance.

### 1.2 ETH/USDC vault release relay — found dead on mainnet, fixed
**Root cause found**: `RELAYER_PRIVATE_KEY_HEX` was never configured on the mainnet node. Bridge-validator-approved releases were accumulating in the queue with nothing ever broadcasting the real Ethereum transaction — confirmed via a live, fully-signed, `PENDING` release sitting unactioned in `/vault/release-queue`.

- **Fix**: a dedicated secp256k1 relayer key was generated **entirely server-side** (never transmitted through this conversation or any tool output — the safety classifier itself blocked me from doing this, correctly, and the user generated it directly via their own Lightsail terminal). Public gas-paying address: `0x5E6dde9078441C893007beC7826FF4A117473A90`.
- **Verified live**: relay is polling every 30s and actively attempting the previously-stuck release.
- **Known limitation on that specific stuck release**: nets $0 to the withdrawer (withdrawal amount was smaller than the release fee at the time) — separate, pre-existing UX gap, not fixed this session.

### 1.3 Governance-parameter staleness tracking (new primitive)
`core.GovernanceParamsStore` gained `SetAt`/`LastSetAt`/`IsStale` — every real write path (`TxAdminSetParam`, a passed `PARAM_CHANGE` proposal, executive-committee param changes) now records **when** a value was last confirmed, not just what it is. Persisted through `state.json` (`GovernanceParamsSetAt`, additive field — old snapshots load fine, just start with no recorded timestamps).

Currently gates `EthUsdCentsPerEth` (24-hour max age) for STETH_CCC1 collateral valuation. This is a reusable primitive — worth considering for `BtcUsdCentsPerBtc` and other price-like params during audit.

### 1.4 Mainnet release-fee miscalibration — found and fixed
`VaultReleaseGasPriceWei` had never been governance-set on mainnet, silently falling back to a hardcoded 20 Gwei default. Real mainnet gas price at time of discovery: **0.084 Gwei — a ~238x overcharge**, live since launch, on every ETH/USDC vault withdrawal. Two fixes:

- **UI gap closed**: `admin.html`'s governance-params dropdown was missing `VaultReleaseGasPriceWei` and three sibling vault params (`VaultEthConfirmations`, `VaultBtcConfirmations`, `VaultBtcReleaseFeeRateSatsPerVByte`) entirely — they were backend-admin-settable but had no UI path. Added.
- **Bounded auto-updater built** (`api/vault_gas_price_oracle.go`, `core.TxVaultGasPriceOracleUpdate`): a narrow, single-purpose background loop that periodically reads real `eth_gasPrice` and nudges `VaultReleaseGasPriceWei` toward it — **deliberately not unsupervised**:
  - Signed by its own dedicated key (`VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX`/`_ADDRESS`), never the admin multisig signers — a leaked oracle key can only ever move this one parameter, never anything else `TxAdminSetParam` governs.
  - **Bounded step**: ±50% of current value per update (`VaultGasPriceOracleMaxStepBasisPoints`).
  - **Bounded rate**: enforced at *apply* time via the same `SetAt`/`LastSetAt` mechanism (§1.3), not just the loop's own ticker — closes the gap where a leaked key could otherwise spam updates through the generic `/tx/submit` endpoint at unlimited speed.
  - **Absolute floor/ceiling**: [0.05, 200] Gwei — no live read or compromised key can push it outside this range.
  - Deployed and confirmed live: `vault gas price oracle loop started - signing as e30f13dfa711...`.
- **Test coverage**: `api/vault_gas_price_oracle_test.go` — step/floor/ceiling clamping (both directions, zero-current recovery), unauthorized-address rejection, out-of-bounds-claim re-clamping at apply time, cooldown enforcement, tampered-payload signature rejection.

---

## 2. Priority focus areas for the auditor

Ranked by how directly they touch real funds:

1. **STETH_CCC1 escrow/release paths** (`api/apply.go`'s `applyLockCollateral`, `creditCollateralTo`, `liquidateLoan`) — real balance movement, newest code in the collateral system.
2. **`collateralValueInYen`'s STETH_CCC1 pricing** and the `stethCcc1WeiNeededForYenValue` inverse — the wei-vs-whole-unit bug class this session found and fixed once already; worth independently re-deriving the math rather than trusting the fix.
3. **The gas-price oracle's authorization boundary** (`applyVaultGasPriceOracleUpdate`) — confirm the three independent checks (address match, cooldown, re-clamp) are each individually sufficient and that none can be bypassed via a crafted transaction.
4. **The vault release relay's gas-paying key custody** — currently a bare hex env var on the server (`RELAYER_PRIVATE_KEY_HEX`), same posture as `BRIDGE_VALIDATOR_PRIVATE_KEY_HEX`. Real production hardening (HSM/KMS) is explicitly out of scope of this session's work and already flagged in `KNOWN_GAPS.md`.
5. **Governance param staleness gate** — confirm 24 hours is an appropriate window, not just a judgment call that happened to feel reasonable.

## 3. What's explicitly NOT covered by this session's work

- Real KMS/HSM custody for `RELAYER_PRIVATE_KEY_HEX`, `BRIDGE_VALIDATOR_PRIVATE_KEY_HEX`, or the new `VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX` — all remain plain server-side env vars.
- A claim/sweep mechanism for the unregistered-lender STETH_CCC1 seizure edge case (§1.1).
- Any change to `LiquidationThresholdPercent`'s flat 120% — STETH_CCC1 shares it with CCT/OETH; a per-symbol override exists in the codebase already (added earlier this session, before this document's scope) but the *value* for STETH_CCC1 was left at the shared default, not independently risk-assessed.
- Full multi-mainnet-node consensus testing for the gas-price oracle loop — currently verified against a single-mainnet-node deployment; if a second mainnet node ever runs this loop independently, both would submit competing update transactions (harmless — the cooldown/bounds are enforced identically by every node applying the transaction, just wasteful) but this hasn't been exercised against real multi-node timing.

## 4. Test suite status

`go build ./...`, `go vet ./...`, and `go test ./...` all pass clean as of this document's timestamp, across `core`, `api`, `network`, and both `cmd/` binaries. New test files this session: `api/steth_collateral_test.go`, `api/vault_gas_price_oracle_test.go`. No existing test was weakened or skipped to make this pass.

## 5. Full historical disclosure list

Every deliberate simplification or known limitation across the entire codebase (not just this session) is catalogued in [`audit/KNOWN_GAPS.md`](../audit/KNOWN_GAPS.md), organized by category, with file pointers. The two newest sections there (`STETH_CCC1 as loan collateral`, appended earlier today) are the ones most relevant to this handoff.

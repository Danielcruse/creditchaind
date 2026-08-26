# CreditChainVault - Audit Readiness Package

**Prepared:** 2026-08-02
**Status of this document:** material for a professional third-party smart-contract
auditor to review. **This is not itself an audit.** No independent security
researcher has reviewed this code. Do not treat anything in this document as
clearance to put real value through the contract on mainnet - see "Hard
preconditions" at the bottom.

## 1. What this contract does

`CreditChainVault.sol` is the on-chain half of a lock-and-mint bridge. A user
locks real ETH or an allowlisted ERC20 (USDC) into the contract; CreditChain's
off-chain network (a set of independent "bridge validators") observes the
lock event, and once a threshold of them agree it happened, mints a 1:1-backed
token (`ETH_CCC1`/`USDC_CCC1`) on CreditChain's own ledger. To withdraw, the
user burns the CCC1 token, the same validators independently sign off on the
release, and once threshold signatures are gathered, anyone can submit them to
`release()`, which verifies the signatures **on-chain** via `ecrecover` and
pays out the real asset.

The design goal: CreditChain's backend servers carry **zero additional trust**
beyond what this contract itself enforces. A malicious or compromised
CreditChain server cannot move funds out of the vault - only a genuine
threshold of the validator set's private keys can produce a signature bundle
this contract will accept.

## 2. Trust model

**What must hold for the system to be safe:**
- At least `threshold` of the `N` validator private keys are never
  simultaneously compromised or colluding.
- The validators' off-chain agreement process (which transactions are real
  locks, which burns are real) is itself correct - this contract has no way
  to verify that independently; it only verifies that a threshold of
  *registered* keys signed a given payload.
- `block.chainid` is correctly bound into every signed digest (deposit,
  release, and every self-governance action), preventing cross-chain replay
  of a signature bundle valid on one deployment against another.

**What this contract does NOT trust:**
- The caller of `release()` (permissionless - anyone can relay a valid
  signature bundle).
- CreditChain's own servers, beyond being one possible relayer.
- Any single validator (threshold, not unanimity, and each signer must be
  distinct - see `_recoverSigner`'s dedup-by-index logic).

## 3. Scope

**In scope for this package:** `contracts/CreditChainVault.sol` (306 lines)
and its test suite `contracts/test/CreditChainVault.t.sol`.

**Explicitly out of scope, needing separate review:**
- The Go backend's off-chain attestation logic (`core/vault_lock_attestation.go`,
  `core/vault_release_approval.go`, `api/vault_eth_watch.go`) - the process by
  which validators decide what to sign. A bug there (e.g. a validator signing
  release of funds for a lock that never happened) is not something this
  contract can catch; the contract only verifies *that* a threshold signed,
  not *that they were right to*.
- The Bitcoin side of the vault (`core/btc_vault.go`, P2WSH multisig
  redeem-script construction) - no smart contract exists for Bitcoin, so this
  is pure off-chain script/witness construction, a different audit
  discipline entirely (Bitcoin Script correctness, not Solidity).
- Key custody for the validators themselves (AWS KMS provisioning, access
  control) - an operational concern, not a code-review one.

## 4. Verified test results (not a substitute for audit)

Run 2026-08-02, Foundry 1.7.1, solc 0.8.35 (updated after adding the
per-depositor cap mechanism):

```
Ran 20 tests for test/CreditChainVault.t.sol:CreditChainVaultTest
[PASS] test_DepositERC20_HappyPathOnceAllowlisted
[PASS] test_DepositERC20_RequiresAllowlistedToken
[PASS] test_DepositERC20_RevertsWhenExceedingPerDepositorCap
[PASS] test_Deposit_AllowsExactlyAtCap
[PASS] test_Deposit_CapDisabledWhenZero
[PASS] test_Deposit_CapIsCumulativeAcrossMultipleDeposits
[PASS] test_Deposit_DifferentDepositorsHaveIndependentCapHeadroom
[PASS] test_Deposit_LocksEthAndEmitsLocked
[PASS] test_Deposit_RevertsOnZeroValue
[PASS] test_Deposit_RevertsWhenExceedingPerDepositorCap
[PASS] test_Pause_BlocksNewDepositsNotRelease
[PASS] test_Release_BlocksReentrancy
[PASS] test_Release_HappyPathWithTwoOfThreeSignatures
[PASS] test_Release_RevertsOnDuplicateSignerNotCountedTwice
[PASS] test_Release_RevertsOnReplayedReleaseId
[PASS] test_Release_RevertsOnWrongSigner
[PASS] test_Release_RevertsWithInsufficientSignatureCount
[PASS] test_SetPerDepositorCaps_RequiresThresholdSignatures
[PASS] test_SetPerDepositorCaps_RevertsWithInsufficientSignatures
[PASS] test_SetValidators_RequiresThresholdSignatures
Suite result: ok. 20 passed; 0 failed; 0 skipped
```

These tests were written by the same process that wrote the contract. They
demonstrate the contract behaves as its author intended - they do not
demonstrate the absence of bugs an adversarial reviewer would look for
(e.g. signature malleability edge cases beyond the low-S check already in
place, gas-griefing via a very large `validators` array, unexpected ERC20
behaviors like fee-on-transfer or non-standard return values on tokens added
to the allowlist later, front-running of `setValidators`/`setTokenAllowed`
nonce reuse, etc.). **An auditor should assume nothing beyond "compiles and
passes its own tests."**

## 5. Deployment history

- Sepolia testnet (chain ID 11155111): `0x467477F749f265b306045bB1e338a843A86a1745`,
  deployed 2026-08-02, constructor args `validators=[0xf4e0649e14c3cfd201f9e13d093f7fa27238152d]`,
  `threshold=1`. Single-validator testnet configuration only - not
  representative of intended production M-of-N security. **Predates the
  per-depositor cap mechanism** - deliberately left as-is (not redeployed),
  so this Sepolia address does NOT have `perDepositorCapWei`/`perDepositorCapUsdc`.
- Mainnet (chain ID 1): deployment in progress as of this document's last
  update - see project memory `project_creditchain_mainnet_node.md` for the
  target node. This deployment DOES include the per-depositor cap,
  initialized at deploy time from an approximate ETH/USD price.

## 6. Known design decisions worth an auditor's explicit attention

- **No upgradeability.** The contract is not proxy-upgradeable; `setValidators`
  is the only mechanism to change signers, and it requires the same
  threshold-signature check as everything else. A bug found post-deployment
  cannot be patched in place - only a new contract + validator-coordinated
  migration.
- **No separate admin/owner key anywhere** - deliberate, but means a
  threshold-signature bug in `_requireThresholdSignatures` would be
  unrecoverable via any privileged path.
- **`release()` is deliberately not gated by `whenNotPaused`** - a paused
  vault still allows withdrawal of already-locked funds via genuine
  threshold-signed releases; only new deposits stop. Confirm this is the
  intended pause semantics, not an oversight.
- **ERC20 allowlist is a simple boolean map**, not per-token risk parameters.
  A malicious or non-standard ERC20 added to the allowlist (accidentally or
  via a compromised threshold) could behave unexpectedly in
  `depositERC20`/`release`'s `transfer`/`transferFrom` calls - USDC is the
  only intended token, but the contract itself doesn't enforce that.
- **A per-depositor cumulative deposit cap now exists on-chain** (`perDepositorCapWei`/
  `perDepositorCapUsdc`, added 2026-08-02, deployed on mainnet only - the
  original Sepolia deployment predates this and does not have it), tracked
  in native units per `creditChainAddress` and enforced in `deposit()`/
  `depositERC20()`. This is a mainnet-safety measure taken **in place of an
  audit**, per explicit operator instruction - not a substitute for one, and
  an auditor should treat it as exactly that: a blast-radius limiter, not
  a correctness guarantee. Two things worth an auditor's explicit attention:
  (1) the cap is in native units (wei / USDC's own 6dp units), not USD -
  deliberately, to avoid adding a price oracle's own manipulation surface -
  so its real-dollar value drifts with market price until an admin action
  (`setPerDepositorCaps`) updates it; (2) it tracks CUMULATIVE LIFETIME
  deposits per depositor, never decremented on withdrawal (release() has no
  way to attribute a withdrawal back to a depositor's creditChainAddress),
  so it is strictly conservative, not a live "current balance at risk"
  figure. **The cap does not limit `release()`'s own blast radius** - a
  bug there could still drain the contract's full actual balance, which is
  bounded only by the sum of what every depositor has been capped to
  individually, not by any single global ceiling.

## 7. Hard preconditions before this touches real value

1. **A professional, independent smart-contract audit** of this file, by a
   reviewer with no involvement in writing it.
2. **Production key custody** for the validator set - each validator's real
   ETH signing key held in a secure system (e.g. AWS KMS
   `KeySpecEccSecgP256k1`), never a developer's local key or "LOCAL DEV
   FALLBACK" mode.
3. A genuine M-of-N validator set (M >= 2) - the only deployment to date is
   single-validator/threshold-1, adequate for testnet exercising only.

None of these are satisfied as of this document's date. Do not treat a clean
`forge test` run, or this document's existence, as authorization to deploy
with real value.

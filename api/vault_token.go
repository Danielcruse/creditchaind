package api

import "github.com/creditchain/creditchaind/core"

// ethCcc1Store/usdcCcc1Store back ETH_CCC1/USDC_CCC1 - the real,
// vault-locked-and-attested tokens from the lock-and-mint bridge
// redesign (see core/vault_lock_attestation.go's doc comment for why
// these are NEW, separate tokens coexisting alongside the legacy
// mirror-model ETHB/USDCTEST, not a rename/migration of them). Uses
// core.DecimalTokenStore (not core.TokenStore's uint64) since wei/
// USDC-6dp amounts need arbitrary precision - see that type's doc
// comment in core/decimal_token_store.go.
var ethCcc1Store = core.NewDecimalTokenStore(18) // wei
var usdcCcc1Store = core.NewDecimalTokenStore(6) // USDC's real decimal count

// stethCcc1Store backs STETH_CCC1 - the vault-locked receipt for real
// Lido stETH, minted through the exact same lock-and-mint attestation
// path as ETH_CCC1/USDC_CCC1 (see api/lido_stake.go's own doc comment
// for the full staking flow this is one piece of). 18 decimals,
// matching stETH's own real decimal count.
var stethCcc1Store = core.NewDecimalTokenStore(18)

// btcCcc1Store backs BTC_CCC1 - satoshis never overflow uint64, so this
// stays a plain core.TokenStore, matching bridgedBtcStore's existing
// precedent (api/token.go).
var btcCcc1Store = core.NewTokenStore()

// bridgeValidatorStore/vaultLockAttestationStore/
// vaultReleaseApprovalStore are the vault bridge's consensus-critical
// authorization state - see core/bridge_validator.go,
// core/vault_lock_attestation.go, core/vault_release_approval.go.
var bridgeValidatorStore = core.NewBridgeValidatorStore()
var vaultLockAttestationStore = core.NewVaultLockAttestationStore()
var vaultReleaseApprovalStore = core.NewVaultReleaseApprovalStore()

func init() {
	btcCcc1Store.Token.Name = "CreditChain Vault BTC"
	btcCcc1Store.Token.Symbol = "BTC_CCC1"
}

package core

import (
	"crypto/sha256"
	"encoding/hex"
)

func GenerateAddress(
	publicKey []byte,
) string {

	hash :=
		sha256.Sum256(
			publicKey,
		)

	return hex.EncodeToString(
		hash[:20],
	)
}

// ETHTestAddressTag/USDCTestAddressTag namespace-separate ETHTEST and
// USDCTEST from a wallet's main CCT address (see DeriveTokenAddress) -
// these are "imported" tokens with their own chain identity, not just
// another balance sitting under the CCT address, so the CCT address
// alone has no way to look up or move them.
const (
	ETHTestAddressTag  = "ETHTEST-SUBADDRESS"
	USDCTestAddressTag = "USDCTEST-SUBADDRESS"
	// BtcTestAddressTag namespaces BTCTEST, the synthetic CCT-paired
	// price-discovery counterpart to ETHTEST (see api/token.go's
	// btcTestStore/cctBtcTestPool doc comment) - same reasoning as
	// ETHTestAddressTag, not to be confused with BTCBAddressTag below
	// (a real bridged balance, not a synthetic pool token).
	BtcTestAddressTag = "BTCTEST-SUBADDRESS"
	YenAddressTag     = "YENE-SUBADDRESS"
	// ETHBAddressTag namespaces the auto-bridge unified ETH balance
	// (see core/eth_custodial.go's doc comment) - deliberately its own
	// tag, not reused from ETHTestAddressTag, since ETHB is a real
	// bridged-balance token backed by a live custodial Sepolia deposit
	// address, not the purely synthetic faucet token ETHTEST is.
	ETHBAddressTag = "ETHB-SUBADDRESS"
	// BTCBAddressTag namespaces the bridged-BTC balance (BTCB) - same
	// reasoning as ETHBAddressTag: a real bridged balance backed by
	// deposits detected against the wallet's self-custodial BTC testnet
	// address (see api/btc_bridge.go), not a synthetic faucet token.
	BTCBAddressTag = "BTCB-SUBADDRESS"
	// ETHCcc1AddressTag/UsdcCcc1AddressTag namespace the vault-backed
	// ETH_CCC1/USDC_CCC1 tokens (see core/vault_lock_attestation.go) -
	// unlike ETHB/BTCB above, resolveBalanceAddress (api/subaddress.go)
	// unifies these with the wallet's real self-custodial Ethereum
	// address rather than deriving a separate hash, since a vault-backed
	// balance genuinely corresponds 1:1 to funds locked FROM that real
	// address. These tags exist only as the fallback path for a wallet
	// that somehow has no self-custodial ETH account yet.
	ETHCcc1AddressTag  = "ETH_CCC1-SUBADDRESS"
	UsdcCcc1AddressTag = "USDC_CCC1-SUBADDRESS"
	// StethCcc1AddressTag namespaces the vault-backed STETH_CCC1 token
	// (real Lido stETH, see api/lido_stake.go) - same unify-with-self-
	// custodial-ETH-address reasoning as ETHCcc1AddressTag/
	// UsdcCcc1AddressTag just above (stETH is a real Ethereum-chain
	// ERC-20 asset, genuinely held at that same real address), this tag
	// is only the fallback path for a wallet with no self-custodial ETH
	// account yet.
	StethCcc1AddressTag = "STETH_CCC1-SUBADDRESS"
	// BtcCcc1AddressTag namespaces the vault-backed BTC_CCC1 token -
	// follows BTCBAddressTag's derived-sub-address precedent (no single
	// unifying "the" BTC address concept exists server-side the way ETH
	// has one).
	BtcCcc1AddressTag = "BTC_CCC1-SUBADDRESS"
)

// DeriveTokenAddress computes a distinct address for a specific
// imported-token namespace from the SAME public key GenerateAddress
// uses for the main CCT address - same keypair authorizes both (no new
// keys to manage), but the two addresses are NOT interchangeable:
// knowing/using the CCT address gives no way to derive or look up the
// tagged address, since the hash only runs forward. Any node can
// independently re-derive this from a transaction's already-verified
// AuthPublicKey, so no extra signing or server-side key storage is
// needed beyond what request auth already provides.
func DeriveTokenAddress(publicKey []byte, tag string) string {
	hash := sha256.Sum256(append([]byte(tag+":"), publicKey...))
	return hex.EncodeToString(hash[:20])
}

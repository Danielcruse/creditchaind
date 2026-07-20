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
	YenAddressTag      = "YENE-SUBADDRESS"
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

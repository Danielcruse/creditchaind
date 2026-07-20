package api

import (
	"encoding/hex"

	"github.com/creditchain/creditchaind/core"
)

// resolveBalanceAddress returns which address a given token symbol's
// balance actually lives under. CCT, REPT, and any developer-registered
// token all stay on the wallet's normal CCT address - only ETHTEST and
// USDCTEST are "imported tokens" with their own derived sub-address
// (see core.DeriveTokenAddress), so the CCT address alone has no
// permission over an ETHTEST/USDCTEST balance. publicKeyHex is always
// the ALREADY-AUTHENTICATED public key behind cctAddress (e.g. a
// transaction's own AuthPublicKey, or a signed request envelope's
// PublicKey) - never a value taken from unverified user input.
func resolveBalanceAddress(cctAddress string, publicKeyHex string, symbol string) string {
	// ETHB is a special case, resolved before the generic derived-tag
	// path below: once a wallet has a real KMS/dev-signer-held custodial
	// Sepolia account (core.EthCustodialAccount), its ETHB ledger key IS
	// that account's real EthAddress - not a second, unrelated derived
	// hash. This is what makes "the same address, whether you're
	// depositing, holding, or sending ETHB" literally true rather than
	// merely true-in-spirit: deposits already land at EthAddress (that's
	// the only address real Sepolia ETH can ever arrive at), so crediting
	// and debiting the SAME string closes the one real gap that existed
	// here. Falls back to the old derived-hash address only if no
	// custodial account exists yet - can't happen in practice for a
	// wallet that already holds ETHB (a custodial account is a deposit
	// prerequisite), kept purely as a defensive default.
	if symbol == "ETHB" {
		if account, ok := ethCustodialStore.Get(cctAddress); ok {
			return account.EthAddress
		}
	}

	var tag string
	switch symbol {
	case "ETHTEST":
		tag = core.ETHTestAddressTag
	case "USDCTEST":
		tag = core.USDCTestAddressTag
	case "YENE":
		tag = core.YenAddressTag
	case "ETHB":
		tag = core.ETHBAddressTag
	case "BTCB":
		tag = core.BTCBAddressTag
	default:
		return cctAddress
	}

	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return cctAddress
	}

	return core.DeriveTokenAddress(pub, tag)
}

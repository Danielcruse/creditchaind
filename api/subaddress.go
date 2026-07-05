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
	var tag string
	switch symbol {
	case "ETHTEST":
		tag = core.ETHTestAddressTag
	case "USDCTEST":
		tag = core.USDCTestAddressTag
	default:
		return cctAddress
	}

	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return cctAddress
	}

	return core.DeriveTokenAddress(pub, tag)
}

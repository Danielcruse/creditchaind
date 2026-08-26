package api

import (
	"encoding/hex"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// normalizeEthAddress lowercases a 0x-prefixed Ethereum address -
// Ethereum addresses are case-INSENSITIVE (EIP-55 checksum casing is a
// client-side typo-detection convention, not a semantically different
// address), but every balance store keyed by one of these strings
// (ethCcc1Store, usdcCcc1Store, bridgedEthStore, ethAddressStore) is a
// plain case-sensitive map. Without normalizing at every entry/read
// point, two differently-cased spellings of the identical real address
// would silently behave as two unrelated accounts - a real gap caught
// in review (website/js/hdwallet.js's deriveEthAddress checksums its
// output; website/js/vendor/eth-tx.js's addressFromPrivateKey does not,
// and both are used to derive "this wallet's ETH address" in different
// parts of this codebase). Call this on every ETH address immediately
// before it's used as a store key, on both the write side
// (api/eth_bridge.go's HandleEthRegisterAddress) and the read side
// (this file's resolveBalanceAddress, api/vault_info.go's
// HandleVaultBalance) - non-hex or malformed input is returned
// unchanged (never this function's job to validate shape).
func normalizeEthAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "0x") || strings.HasPrefix(addr, "0X") {
		return "0x" + strings.ToLower(addr[2:])
	}
	return addr
}

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
	// USDCTEST is unified the same way as ETHB just above - USDC is a
	// real Ethereum-chain (ERC-20) asset, so it belongs at the same
	// address as any other Ethereum-side asset this wallet holds, not
	// a separate CreditChain-only derived hash. This is what makes "one
	// wallet can receive and send USDC between other users/wallets
	// exactly like it does elsewhere" literally true - the address is
	// the same real Ethereum address other ETH/USDC wallets would use.
	if symbol == "ETHB" || symbol == "USDCTEST" || symbol == "ETH_CCC1" || symbol == "USDC_CCC1" || symbol == "STETH_CCC1" {
		// ethAddressStore (the free, self-custodial watched-address path
		// - see core/eth_address.go's doc comment) takes priority over
		// ethCustodialStore (the older paid AWS-KMS path, kept only for
		// backward compatibility with any account provisioned before
		// this changed) - every current UI flow registers into
		// ethAddressStore, never creates a new custodial account.
		// ETH_CCC1/USDC_CCC1 (the vault-backed tokens) unify with this
		// same real self-custodial address for the same reason ETHB
		// does: a vault-backed balance genuinely corresponds 1:1 to
		// funds locked FROM that real address, so it belongs at the
		// same address, not a separate derived hash.
		if watched, ok := ethAddressStore.Get(cctAddress); ok {
			return normalizeEthAddress(watched.EthAddress)
		}
		if account, ok := ethCustodialStore.Get(cctAddress); ok {
			return normalizeEthAddress(account.EthAddress)
		}
	}

	var tag string
	switch symbol {
	case "ETHTEST":
		tag = core.ETHTestAddressTag
	case "BTCTEST":
		tag = core.BtcTestAddressTag
	case "USDCTEST":
		tag = core.USDCTestAddressTag
	case "YENE":
		tag = core.YenAddressTag
	case "ETHB":
		tag = core.ETHBAddressTag
	case "BTCB":
		tag = core.BTCBAddressTag
	case "ETH_CCC1":
		tag = core.ETHCcc1AddressTag
	case "USDC_CCC1":
		tag = core.UsdcCcc1AddressTag
	case "STETH_CCC1":
		tag = core.StethCcc1AddressTag
	case "BTC_CCC1":
		tag = core.BtcCcc1AddressTag
	default:
		return cctAddress
	}

	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return cctAddress
	}

	return core.DeriveTokenAddress(pub, tag)
}

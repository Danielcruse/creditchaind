package api

import "math/big"

// This file is the CCC1 (ETH_CCC1/USDC_CCC1/BTC_CCC1) equivalent of
// the CCT transfer fee (see applyTransfer's CCT branch) and the
// existing ETHB/BTCB "network fee" branches - a plain wallet-to-wallet
// send of a real, 1:1-backed vault token should cost something close
// to what sending that token actually costs on its real chain (gas),
// charged in the SAME token being sent, split the same 50% Foundation
// / 30% burn / 20% validator way every other platform fee already is
// (see splitPlatformFee) rather than a separate fixed-address sink.
// Before this, a plain ETH_CCC1/USDC_CCC1/BTC_CCC1 transfer was
// entirely fee-free (only marketplace trades against these symbols
// were fee'd) - see applyTransfer's fall-through case for these three
// symbols prior to this file's addition.

// Default*TransferFeeUsdCents are rough approximations of real-chain
// gas cost, not live oracle reads - an admin can override any of these
// via the standard governance-param mechanism
// (api/admin_params.go's adminSettableParams) as real gas trends
// change, same "admin-set approximation, not a live feed" choice
// already made for EthUsdCentsPerEth/BtcUsdCentsPerBtc.
const (
	DefaultEthCcc1TransferFeeUsdCents  uint64 = 300 // ~$3.00, a typical ETH mainnet transfer's gas cost
	DefaultUsdcCcc1TransferFeeUsdCents uint64 = 200 // ~$2.00, an ERC-20 USDC transfer's usually-higher gas cost
	DefaultBtcCcc1TransferFeeUsdCents  uint64 = 150 // ~$1.50, a typical BTC network fee
)

// ccc1TransferFeeSmallest computes the fee to charge for one plain
// transfer of symbol, in that same symbol's smallest unit. ETH_CCC1/
// BTC_CCC1 convert their admin-set USD-cent fee through the SAME
// EthUsdCentsPerEth/BtcUsdCentsPerBtc governance prices
// account_holdings_cap.go already established (usdCentsToWei/
// usdCentsToSats) - if that price hasn't been admin-set yet (0), this
// returns 0 (no fee), the same fail-open convention
// accountHoldingsRatesSet already uses, rather than charging a
// nonsensical amount or blocking transfers until an admin remembers to
// set a price. USDC_CCC1 needs no such price - it's a USD stablecoin
// (assumed 1:1 pegged), so its USD-cent fee converts to its 6-decimal
// smallest unit with a fixed multiplier, no governance price involved.
func ccc1TransferFeeSmallest(symbol string) uint64 {
	switch symbol {
	case "ETH_CCC1":
		feeUsdCents := governanceParamsStore.GetOrDefault("EthCcc1TransferFeeUsdCents", DefaultEthCcc1TransferFeeUsdCents)
		centsPerEth := governanceParamsStore.GetOrDefault("EthUsdCentsPerEth", 0)
		return usdCentsToWei(new(big.Int).SetUint64(feeUsdCents), centsPerEth)
	case "BTC_CCC1":
		feeUsdCents := governanceParamsStore.GetOrDefault("BtcCcc1TransferFeeUsdCents", DefaultBtcCcc1TransferFeeUsdCents)
		centsPerBtc := governanceParamsStore.GetOrDefault("BtcUsdCentsPerBtc", 0)
		return usdCentsToSats(new(big.Int).SetUint64(feeUsdCents), centsPerBtc)
	case "USDC_CCC1":
		feeUsdCents := governanceParamsStore.GetOrDefault("UsdcCcc1TransferFeeUsdCents", DefaultUsdcCcc1TransferFeeUsdCents)
		return feeUsdCents * 10_000 // 1 USD cent = 0.01 USD = 10,000 of USDC_CCC1's 6-decimal smallest unit
	default:
		return 0
	}
}

func isCcc1TransferFeeSymbol(symbol string) bool {
	return symbol == "ETH_CCC1" || symbol == "USDC_CCC1" || symbol == "BTC_CCC1"
}

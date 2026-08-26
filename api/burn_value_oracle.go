package api

import "math/big"

// This file lets a non-CCT platform-fee burn (ETH_CCC1/BTC_CCC1/
// USDC_CCC1) feed core.Treasury.CCTBurnedPendingReward - the pool/fee
// mix of all platform-fee revenue was always meant to back the block
// reward, not just the CCT-denominated slice of it. Per the same
// "admin-set approximation, not a live oracle" precedent already
// established for EthUsdCentsPerEth/BtcUsdCentsPerBtc
// (api/account_holdings_cap.go) and the CCC1 transfer fee
// (api/ccc1_transfer_fee.go) - this deliberately never reads
// api/price_oracle.go's node-local, independently-polled live price
// cache. That cache is explicitly documented as "not gossiped/
// consensus state" - two nodes applying the identical block would
// compute different burn-value/reward amounts if this used it,
// exactly the kind of divergence that has already forked this chain
// twice (see audit/KNOWN_GAPS.md). CctUsdCents is instead a plain
// admin-settable governance param (api/admin_params.go), replicated
// identically to every node the same way every other USD-cents rate
// in this codebase already is.

// usdCentsToCctWhole converts a USD-cents value into whole CCT, given
// an admin-set USD-cents-per-whole-CCT rate - the CCT-denominated
// mirror of usdCentsToWei/usdCentsToSats (api/account_holdings_cap.go),
// except the output stays in CCT's OWN whole-unit convention (no
// further scale-up) since core.Treasury.CCTBurnedPendingReward is
// itself whole CCT, not smallest-unit. Fails open to 0 (never a
// fabricated amount) if either input is unset/non-positive - same
// "0 = not configured yet" convention as every other rate in this
// file's family.
func usdCentsToCctWhole(cents *big.Int, cctUsdCents uint64) uint64 {
	if cents.Sign() <= 0 || cctUsdCents == 0 {
		return 0
	}
	v := new(big.Int).Div(cents, new(big.Int).SetUint64(cctUsdCents))
	if !v.IsUint64() {
		return ^uint64(0)
	}
	return v.Uint64()
}

// cctEquivalentWholeOfBurn values amount (symbol's own native smallest
// unit - wei for ETH_CCC1, satoshis for BTC_CCC1, USDC_CCC1's
// 6-decimal smallest unit) in whole CCT terms, for splitPlatformFee's
// burn share to add to core.Treasury.CCTBurnedPendingReward alongside
// real CCT burns. Chained entirely through admin-set USD-cents rates
// (never api/price_oracle.go's live cache - see this file's top doc
// comment) via the same weiToUsdCents/satsToUsdCents helpers
// api/account_holdings_cap.go already established.
//
// Returns 0 (contributes nothing, never blocks the burn itself) if
// CctUsdCents or the symbol's own USD rate hasn't been admin-set yet,
// or for any symbol with no defined USD/CCT exchange rate at all
// (REPT - a reputation-progression token, not a priced asset). This
// mirrors ccc1TransferFeeSmallest's fail-open convention: an
// unconfigured rate undercounts the reward, it never fabricates one or
// blocks the fee split.
func cctEquivalentWholeOfBurn(symbol string, amount uint64) uint64 {
	if amount == 0 {
		return 0
	}
	cctUsdCents := governanceParamsStore.GetOrDefault("CctUsdCents", 0)
	if cctUsdCents == 0 {
		return 0
	}

	var usdCents *big.Int
	switch symbol {
	case "ETH_CCC1":
		centsPerEth := governanceParamsStore.GetOrDefault("EthUsdCentsPerEth", 0)
		if centsPerEth == 0 {
			return 0
		}
		usdCents = weiToUsdCents(amount, centsPerEth)
	case "BTC_CCC1":
		centsPerBtc := governanceParamsStore.GetOrDefault("BtcUsdCentsPerBtc", 0)
		if centsPerBtc == 0 {
			return 0
		}
		usdCents = satsToUsdCents(amount, centsPerBtc)
	case "USDC_CCC1":
		// USD stablecoin, assumed 1:1 pegged - same convention
		// ccc1TransferFeeSmallest's USDC_CCC1 branch already uses. 6
		// decimals: 10,000 of its smallest unit = 1 USD cent.
		usdCents = new(big.Int).SetUint64(amount / 10_000)
	default:
		return 0
	}

	return usdCentsToCctWhole(usdCents, cctUsdCents)
}

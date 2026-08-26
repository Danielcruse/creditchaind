package api

import (
	"math/big"

	"github.com/creditchain/creditchaind/core"
)

// This file fixes a real unit-scale bug in Hub Rate trading
// (api/hub_rate.go, api/apply.go's applyHubTradeRequest/
// settleHubTradeOnChain): core.HubRate.SellRate/BuyRate is defined as
// "fiat amount per 1 WHOLE unit of Symbol, scaled by
// core.HubRateScale" (see core/hub_rate.go), but every hub-trade
// Amount flows straight into resolveTokenStore's BalanceOf/Transfer,
// which for ETH_CCC1/USDC_CCC1/BTC_CCC1 operate in that token's own
// SMALLEST unit (wei/6-decimal/sats) - a mismatch the old
// `amount * rate / core.HubRateScale` formula never accounted for.
// Before this fix, the only amounts that ever "worked" were small
// integers that were actually being interpreted as a handful of wei/
// sats - not a usable trade size, and never fractional at all (see
// website/js/p2p.js's onHubCardAmountChange, fixed alongside this).

// hubTradeAmountDecimals reports how many smallest-unit decimal places
// resolveTokenStore's adapter for symbol operates in - 0 for CCT/REPT/
// any other whole-unit-native token (matches cctWholeUnitAdapter's own
// "already whole-unit" convention), 18 for ETH_CCC1 (wei), 6 for
// USDC_CCC1, 8 for BTC_CCC1 (sats).
func hubTradeAmountDecimals(symbol string) uint64 {
	switch symbol {
	case "ETH_CCC1":
		return 18
	case "USDC_CCC1":
		return 6
	case "BTC_CCC1":
		return 8
	default:
		return 0
	}
}

func pow10(exp uint64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(exp), nil)
}

// hubTradeFiatAmountSmallest converts a hub-trade amount (already in
// symbol's own smallest unit) into the REAL counter token's own
// smallest unit, given a rate quoted as "fiat WHOLE units per 1 SYMBOL
// WHOLE unit" scaled by core.HubRateScale. Used only for ONCHAIN mode,
// where fiatSymbol is itself a real, resolvable token that needs its
// own smallest-unit precision (e.g. trading ETH_CCC1 against
// USDC_CCC1) - see settleHubTradeOnChain and HandleRequestHubTrade's
// ONCHAIN fast-fail checks. big.Int throughout: amount*rate can well
// exceed uint64 at realistic wei-scale trade sizes long before it
// exceeds a real balance.
func hubTradeFiatAmountSmallest(amount uint64, symbol string, rate uint64, fiatSymbol string) uint64 {
	numerator := new(big.Int).Mul(new(big.Int).SetUint64(amount), new(big.Int).SetUint64(rate))
	numerator.Mul(numerator, pow10(hubTradeAmountDecimals(fiatSymbol)))

	denominator := new(big.Int).Mul(big.NewInt(int64(core.HubRateScale)), pow10(hubTradeAmountDecimals(symbol)))
	if denominator.Sign() == 0 {
		return 0
	}

	result := new(big.Int).Div(numerator, denominator)
	if !result.IsUint64() {
		return ^uint64(0) // clamp rather than silently wrap - should never actually happen at realistic trade sizes
	}
	return result.Uint64()
}

// hubTradeFiatAmountWhole is the OFFAPP-mode equivalent -
// rate.FiatSymbol there is free-text (e.g. "NGN"), never a real token,
// so the result is a plain whole-fiat-currency-unit display value
// (the resulting P2POffer's WantAmount), never itself moved through a
// token store - see applyHubTradeRequest's OFFAPP branches.
func hubTradeFiatAmountWhole(amount uint64, symbol string, rate uint64) uint64 {
	numerator := new(big.Int).Mul(new(big.Int).SetUint64(amount), new(big.Int).SetUint64(rate))
	denominator := new(big.Int).Mul(big.NewInt(int64(core.HubRateScale)), pow10(hubTradeAmountDecimals(symbol)))
	if denominator.Sign() == 0 {
		return 0
	}
	result := new(big.Int).Div(numerator, denominator)
	if !result.IsUint64() {
		return ^uint64(0)
	}
	return result.Uint64()
}

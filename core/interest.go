package core

// InterestRate is keyed off Wallet.Reputation (1-100 scale), aligned
// with the CreditTier breakpoints - higher tiers unlock lower rates,
// per the "Credit Tiers unlock lower interest" requirement. This is
// the reputation-only BASE rate; a loan backed by price-bearing
// collateral (oETH - see PriceAdjustedInterestRate) layers a market
// signal on top of it.
func InterestRate(reputation uint64) uint64 {
	switch TierForReputation(reputation) {
	case TierGenesis:
		return 3
	case TierDiamond:
		return 5
	case TierGold:
		return 8
	case TierSilver:
		return 12
	default: // TierBronze
		return 20
	}
}

// PriceTrendLookbackPoints bounds how far back PriceAdjustedInterestRate
// looks for a comparison point - a short window so the rate reacts to
// a genuinely recent price move, not the collateral token's entire
// trading history.
const PriceTrendLookbackPoints = 10

// PriceRiskPremiumMaxPercent caps how many percentage points a falling
// (or rising) collateral price can add to (or take off) the base rate -
// a real lending protocol's variable rate does move with market
// conditions, but an uncapped swing would let one steep price tick
// make a loan's terms absurd.
const PriceRiskPremiumMaxPercent = 15

// MinInterestRatePercent floors the final rate after a price discount
// is applied - a loan should never round down to 0% (or go negative)
// just because collateral happened to be rallying.
const MinInterestRatePercent = 1

// PriceAdjustedInterestRate layers a collateral-price-based risk
// premium/discount on top of a reputation-tier base rate (see
// InterestRate) - the "dependent on price, like a real lending
// protocol" half of self-funded borrow's rate: reputation still sets
// the baseline (this project's existing risk signal), but the
// collateral asset's own recent AMM price trend now moves it up or
// down from there - a loan backed by collateral that's been losing
// value gets more expensive, mirroring how a real protocol prices
// risk to its liquidity rather than charging every borrower the same
// flat rate regardless of what's actually backing the loan.
//
// history must be oldest-first (PriceHistoryStore.Get's contract).
// Fewer than 2 points means there's no trend yet to react to, so the
// base rate is returned unchanged - this is the common case for a
// symbol that hasn't traded yet.
func PriceAdjustedInterestRate(baseRate uint64, history []PricePoint) uint64 {
	if len(history) < 2 {
		return baseRate
	}

	lookback := PriceTrendLookbackPoints
	if lookback > len(history) {
		lookback = len(history)
	}
	older := history[len(history)-lookback]
	latest := history[len(history)-1]

	if older.Price == 0 {
		return baseRate
	}

	// Positive = price fell since `older` (risk premium), negative =
	// price rose (discount).
	percentChange := (int64(older.Price) - int64(latest.Price)) * 100 / int64(older.Price)

	premium := percentChange
	if premium > PriceRiskPremiumMaxPercent {
		premium = PriceRiskPremiumMaxPercent
	}
	if premium < -PriceRiskPremiumMaxPercent {
		premium = -PriceRiskPremiumMaxPercent
	}

	adjusted := int64(baseRate) + premium
	if adjusted < MinInterestRatePercent {
		adjusted = MinInterestRatePercent
	}
	return uint64(adjusted)
}

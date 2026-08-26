package core

// LiquidationThresholdPercent is how far above what's owed (see
// api/apply.go's repayAmountFor) an OETH-collateralized loan's
// collateral value must stay before proactive liquidation triggers
// (see api/apply.go's processCollateralLiquidations) - 120 means
// collateral must be worth at least 120% of the outstanding debt.
// A plain protocol constant for this phase, not admin-settable like
// Treasury's sale price - this is a safety parameter, not an
// externally observed fact an admin tracks day-to-day, and making it
// instantly mutable would let one transaction retroactively change
// every open loan's liquidation risk with no borrower recourse before
// a governance dashboard exists to make that accountable. Kept as the
// default every symbol without its own entry in
// liquidationThresholdPercentBySymbol uses - see
// LiquidationThresholdPercentFor.
const LiquidationThresholdPercent = 120

// StethCcc1LiquidationThresholdPercent is STETH_CCC1's own, stricter
// margin - 130% rather than the 120% every other collateral type uses.
// STETH_CCC1 is real, mainnet money (unlike OETH's synthetic AMM-priced
// receipt), and even with collateralValueInYen's depeg haircut
// (api/apply.go's StethEthRatioBasisPoints) already applied, a real
// asset backing a real loan warrants a wider safety margin against
// price-feed lag (EthUsdCentsPerEth is admin-refreshed, not a live
// tick-by-tick oracle) and depeg moves sharper than the currently-set
// haircut anticipates. A judgment call, not derived from any external
// risk model - deliberately a hardcoded constant rather than an
// admin-settable governance param, for the exact same
// no-instant-retroactive-risk-change reasoning
// LiquidationThresholdPercent's own doc comment gives; tightening it
// further requires a code change and redeploy, not a single
// transaction.
const StethCcc1LiquidationThresholdPercent = 130

// liquidationThresholdPercentBySymbol holds every collateral symbol
// whose margin differs from the LiquidationThresholdPercent default -
// deliberately sparse (most symbols don't need an entry) and, like
// both constants above, a compile-time table rather than anything
// governance/admin can touch at runtime.
var liquidationThresholdPercentBySymbol = map[string]uint64{
	"STETH_CCC1": StethCcc1LiquidationThresholdPercent,
}

// LiquidationThresholdPercentFor returns the liquidation margin for a
// specific collateral symbol - LiquidationThresholdPercent (120) unless
// that symbol has its own stricter entry in
// liquidationThresholdPercentBySymbol above. Every liquidation-risk
// comparison in this codebase should call this rather than reading
// LiquidationThresholdPercent directly, so a future symbol-specific
// override is a one-line addition to the map, not a hunt through every
// call site.
func LiquidationThresholdPercentFor(symbol string) uint64 {
	if pct, ok := liquidationThresholdPercentBySymbol[symbol]; ok {
		return pct
	}
	return LiquidationThresholdPercent
}

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
// a governance dashboard exists to make that accountable.
const LiquidationThresholdPercent = 120

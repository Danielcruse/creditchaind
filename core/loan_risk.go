package core

// LoanRiskStage is a loan's position in the Default Protection sequence:
// Healthy -> Warning -> MarginCall -> GracePeriod -> (liquidate) ->
// AwaitingFinalRedemption -> Defaulted. See api/apply.go's
// advanceLoanLifecycle for the transition logic and
// processFinalRedemptions for the terminal step.
type LoanRiskStage string

const (
	// LoanRiskHealthy is the zero value - every loan that predates this
	// feature is implicitly Healthy with no migration needed.
	LoanRiskHealthy                 LoanRiskStage = ""
	LoanRiskWarning                 LoanRiskStage = "WARNING"
	LoanRiskMarginCall              LoanRiskStage = "MARGIN_CALL"
	LoanRiskGracePeriod             LoanRiskStage = "GRACE_PERIOD"
	LoanRiskAwaitingFinalRedemption LoanRiskStage = "AWAITING_FINAL_REDEMPTION"
	LoanRiskDefaulted               LoanRiskStage = "DEFAULTED"
)

const (
	// LoanRiskWarningDwellSeconds/LoanRiskMarginCallDwellSeconds are
	// judgment calls - the Default Protection spec gives no explicit
	// Warning/Margin Call dwell times, only Grace Period and Final
	// Redemption, which ARE locked below and must not be tuned without
	// re-confirming with the product decision this came from.
	LoanRiskWarningDwellSeconds    = 24 * 60 * 60
	LoanRiskMarginCallDwellSeconds = 48 * 60 * 60

	// LoanRiskGracePeriodSeconds is LOCKED at the spec's explicit "72
	// hours" example.
	LoanRiskGracePeriodSeconds = 72 * 60 * 60

	// LoanRiskFinalRedemptionWindowSeconds is LOCKED at the spec's
	// explicit "24 hours" example.
	LoanRiskFinalRedemptionWindowSeconds = 24 * 60 * 60
)

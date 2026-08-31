package api

import "github.com/creditchain/creditchaind/core"

// governanceParamsStore seeds with today's hardcoded fee defaults so
// every read has a real value from process start, before any
// PARAM_CHANGE proposal has ever passed - see
// core.GovernanceParamsStore's doc comment for why this starts with
// only these two keys, not a full constants migration.
var governanceParamsStore = core.NewGovernanceParamsStore()

func init() {
	governanceParamsStore.Seed("ValidatorRegisterFeeCCT", ValidatorRegisterFeeCCT)
	governanceParamsStore.Seed("CCTTransferFee", CCTTransferFee)
	governanceParamsStore.Seed("CCTTransferFeeSmallestUnit", DefaultCCTTransferFeeSmallestUnit)

	governanceParamsStore.Seed("TSIWeightRepaymentBps", core.DefaultTrustWeights.RepaymentBps)
	governanceParamsStore.Seed("TSIWeightTradingBps", core.DefaultTrustWeights.TradingBps)
	governanceParamsStore.Seed("TSIWeightGovernanceBps", core.DefaultTrustWeights.GovernanceBps)
	governanceParamsStore.Seed("TSIWeightContributionBps", core.DefaultTrustWeights.ContributionBps)
	governanceParamsStore.Seed("TSIWeightVerificationBps", core.DefaultTrustWeights.VerificationBps)
	governanceParamsStore.Seed("TSIWeightDisputeBps", core.DefaultTrustWeights.DisputeBps)

	governanceParamsStore.Seed(AdFeeBurnBpsKey, DefaultAdFeeBurnBps)
	governanceParamsStore.Seed(AdFeeValidatorBpsKey, DefaultAdFeeValidatorBps)
	governanceParamsStore.Seed(AdFeeTreasuryBpsKey, DefaultAdFeeTreasuryBps)
	governanceParamsStore.Seed(AdFeeModeratorBpsKey, DefaultAdFeeModeratorBps)
	governanceParamsStore.Seed(AdFeeGovFundBpsKey, DefaultAdFeeGovFundBps)
}

// syncTrustWeightIfNeeded is the second half of every write into
// governanceParamsStore for one of the six TSIWeight*Bps keys -
// core.IncreaseWalletTrust/DecreaseWalletTrust (core/reputation.go)
// read core.DefaultTrustWeights directly, a core-package mutable var,
// since core has no way to read this package's governanceParamsStore
// (same layering reasoning as TrustDimensionsCutoverUnixSeconds -
// cutover constants and now this live directly in api/, not core).
// Mirrors core.ActivatedProtocolVersion's exact precedent: a
// governance-settable core-level var, written directly by whichever
// apply-time function actually confirms the change (see
// finalizeProposal's ProposalKindVersionActivation case) rather than
// only living inside governanceParamsStore's own generic map. Called
// from both write paths - the admin fast path
// (applyAdminSetParam) and the community PARAM_CHANGE proposal path
// (finalizeProposal) - so neither can silently diverge from the other.
// A key that isn't one of the six is a no-op.
func syncTrustWeightIfNeeded(key string, value uint64) {
	switch key {
	case "TSIWeightRepaymentBps":
		core.DefaultTrustWeights.RepaymentBps = value
	case "TSIWeightTradingBps":
		core.DefaultTrustWeights.TradingBps = value
	case "TSIWeightGovernanceBps":
		core.DefaultTrustWeights.GovernanceBps = value
	case "TSIWeightContributionBps":
		core.DefaultTrustWeights.ContributionBps = value
	case "TSIWeightVerificationBps":
		core.DefaultTrustWeights.VerificationBps = value
	case "TSIWeightDisputeBps":
		core.DefaultTrustWeights.DisputeBps = value
	}
}

// governanceParam reads a governance-overridable parameter, falling
// back to the given hardcoded default if it was never seeded/set
// (defensive only - init() above always seeds both keys this
// codebase currently reads this way, so the fallback path is not
// expected to actually run in practice).
func governanceParam(key string, fallback uint64) uint64 {
	if v, ok := governanceParamsStore.Get(key); ok {
		return v
	}
	return fallback
}

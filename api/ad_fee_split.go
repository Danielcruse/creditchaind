package api

// AdFee*BpsKey are the five governance-param keys a
// core.ProposalKindParamChange proposal targets to change the
// Sponsored Post fee split (see splitAdFee below) - declared here, not
// in core, for the exact layering reason moderatorFeeParamKey/
// moderatorStakeParamKey are declared in api/moderators.go rather than
// core: governanceParamsStore itself is an api-layer var (core has no
// business importing it), and both the seeding pass
// (api/governance_params.go's init()) and the reading pass (splitAdFee)
// need to agree on the exact same key strings, so they're declared
// once, here, and reused verbatim by both.
//
// Unlike adminSettableParams (api/admin_params.go's single-admin fast
// path), a core.ProposalKindParamChange proposal has NO key allowlist
// at all (see finalizeProposal, api/apply.go) - these five keys are
// already votable today with zero further changes to the voting
// system itself.
const (
	AdFeeBurnBpsKey      = "AdFeeBurnBps"
	AdFeeValidatorBpsKey = "AdFeeValidatorBps"
	AdFeeTreasuryBpsKey  = "AdFeeTreasuryBps"
	AdFeeModeratorBpsKey = "AdFeeModeratorBps"
	AdFeeGovFundBpsKey   = "AdFeeGovFundBps"
)

// Default*Bps are the starting split, seeded into governanceParamsStore
// at process start (api/governance_params.go's init(), same pattern as
// every other seeded key) - a disclosed starting point for the FIRST
// AdFeeBurnBps/etc. governance vote to confirm or change, not a locked
// value. Sums to 10000 (100%).
const (
	DefaultAdFeeBurnBps      = 4000
	DefaultAdFeeValidatorBps = 3000
	DefaultAdFeeTreasuryBps  = 1500
	DefaultAdFeeModeratorBps = 1000
	DefaultAdFeeGovFundBps   = 500
)

// adFeeGovFundAddress reuses the SAME real, already-minted Governance
// Treasury genesis pool (genesisPoolAddress(genesisPoolGovernanceTreasury),
// api/tokenomics.go) rather than inventing a second "governance fund"
// concept - one governance-controlled destination, not two competing
// ones.
func adFeeGovFundAddress() string {
	return genesisPoolAddress(genesisPoolGovernanceTreasury)
}

// splitAdFee divides a Sponsored Post's CCT fee per the governance-
// voted AdFee*Bps split (governanceParam falls back to the Default*Bps
// constants above until a PARAM_CHANGE proposal for that key has ever
// passed). validatorID is "" for a global-reach post - since there's
// no specific validator to pay in that case, the validator share rolls
// into the governance fund instead of being silently dropped (a global
// post still burns/funds treasury/moderators exactly the same as a
// validator-scoped one).
//
// The moderator share is pooled into platformFeeModeratorPoolAddress
// ("CCT") - the EXACT SAME pool the marketplace platform fee's
// moderator share already pools into (see splitPlatformFee), swept
// biweekly to every active moderator weighted by reputation x uptime
// (sweepModeratorFeePoolIfDue) - reviewing a Sponsored Post is real
// moderator work, so it draws from the same reward mechanism reviewing
// a Trading Hub application or resolving a trade dispute already does,
// rather than a second, parallel sweep.
//
// store must be CCT's wholeUnitStore (resolveTokenStore("CCT")) -
// Sponsored Posts are CCT-only in v1, unlike the multi-symbol platform
// fee, so this takes no symbol parameter.
func splitAdFee(store wholeUnitStore, from, validatorID string, fee uint64) {
	if fee == 0 {
		return
	}

	burnShare := fee * governanceParam(AdFeeBurnBpsKey, DefaultAdFeeBurnBps) / 10000
	validatorShare := fee * governanceParam(AdFeeValidatorBpsKey, DefaultAdFeeValidatorBps) / 10000
	treasuryShare := fee * governanceParam(AdFeeTreasuryBpsKey, DefaultAdFeeTreasuryBps) / 10000
	moderatorShare := fee * governanceParam(AdFeeModeratorBpsKey, DefaultAdFeeModeratorBps) / 10000
	// Remainder covers rounding - currently exactly the configured
	// AdFeeGovFundBpsKey share.
	govFundShare := fee - burnShare - validatorShare - treasuryShare - moderatorShare

	// A global post (validatorID == "") has no specific validator to
	// pay - its share rolls into the governance fund alongside
	// govFundShare, in the SAME transfer below, rather than a separate
	// transfer of the same destination.
	if validatorID == "" {
		govFundShare += validatorShare
		validatorShare = 0
	}

	if burnShare > 0 {
		store.Burn(from, burnShare)
		treasuryStore.Treasury.CCTBurnedPendingReward += burnShare
	}
	if validatorShare > 0 {
		store.Transfer(from, validatorID, validatorShare)
	}
	if treasuryShare > 0 {
		store.Transfer(from, treasuryStore.Treasury.FoundationAddress, treasuryShare)
	}
	if moderatorShare > 0 {
		store.Transfer(from, platformFeeModeratorPoolAddress("CCT"), moderatorShare)
	}
	if govFundShare > 0 {
		store.Transfer(from, adFeeGovFundAddress(), govFundShare)
	}
}

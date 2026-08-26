package api

import "github.com/creditchain/creditchaind/core"

// votingWeight computes a wallet's real, live governance vote weight -
// same "derived live, never stored" convention as Mining Power/
// Borrowing Power (api/mining_power.go, api/borrowing_power.go).
// Wallet.VotingPower stays exactly what it always was (a one-time
// 0/1 "activated governance" switch, flipped by applyGovernance's
// "activate" case) - this is what actually gets used as vote weight
// once a wallet has flipped that switch.
//
// referralVotingBonusCap caps how much extra weight top referrers get
// - a flat additive bonus on top of the existing reputation/stake
// formula (not a rebalancing of the existing 60/40 split, which stays
// exactly as documented above). A judgment call ("top community users
// have more voting rights"), not derived from any specified model -
// deliberately small relative to the base formula's typical range
// (roughly 1-100) so referrals nudge weight rather than dominate it.
const referralVotingBonusCap = 20

// 60% reputation / 40% stake - reputation-dominant, matching PoSR's
// own 55%-reputation-weighted philosophy ("not Proof of Richest," see
// core/consensus.go). core.StakeCap is reused from PoSR for the same
// stake-normalization reference point, for philosophical consistency:
// "meaningfully staked" means the same amount whether for consensus
// or governance. Floor of 1 so any activated wallet can still cast a
// meaningful vote even with zero stake - judgment call, not derived
// from a specified model.
func votingWeight(address string) uint64 {
	wallet := walletStore.Get(address)
	if wallet.VotingPower == 0 {
		return 0
	}
	// Governance Trust specifically, not overall TSI - vote weight
	// should scale with demonstrated governance conduct (see trust.go's
	// mapping), not marketplace trading or an unrelated dispute. See
	// core.TrustDimensionValue's doc comment for why no cutover check is
	// needed here.
	return votingWeightUsing(address, core.TrustDimensionValue(wallet, core.DimensionGovernance))
}

// NodeAdmissionMinConviction gates ANY weight from counting at all on
// a NODE_ADMISSION vote - deliberately dropping votingWeightUsing's
// floor-of-1 convenience for this one kind. Without this, wallet
// creation and governance activation being free would let an attacker
// mint N throwaway wallets, each contributing a free floor-weight of
// 1, diluting or passing an admission vote through sheer wallet count
// rather than genuine accumulated trust - a Sybil attack AGAINST the
// Sybil-resistance mechanism this vote exists to run. A judgment call,
// not derived from a specified model.
const NodeAdmissionMinConviction = 10

// votingWeightForNodeAdmission is the ONLY vote-weight function that
// reads Wallet.GovernanceConviction instead of live GovernanceTrust -
// used exclusively for ProposalKindNodeAdmission votes (see
// applyProposalVoteHeadcount's kind-routing, api/apply.go).
// Deliberately NOT a change to the general votingWeight formula above:
// (1) core.GovernanceParamsStore's own doc comment already warns
// against exactly this kind of scope-creep, citing a real past
// production incident; (2) the general 60/40 split was JUST
// restructured this session (the TSI work) - changing it again
// immediately for an unrelated feature would make any regression hard
// to attribute to either change; (3) a general swap would collapse
// existing highly-trusted wallets' weight on UNRELATED votes (param
// changes, disputes) the instant this ships, before they've had weeks
// to accumulate conviction - a strictly larger blast radius than this
// feature needs; (4) node admission is uniquely the vote where "who
// gets to decide" is itself Sybil-relevant - ordinary proposals don't
// grant new attack surface (network membership) as their own output.
func votingWeightForNodeAdmission(address string) uint64 {
	wallet := walletStore.Get(address)
	if wallet.VotingPower == 0 {
		return 0
	}
	if wallet.GovernanceConviction < NodeAdmissionMinConviction {
		return 0
	}
	return votingWeightUsing(address, wallet.GovernanceConviction)
}

// votingWeightUsing factors out the stake-normalization/referral-bonus
// logic votingWeight and votingWeightForNodeAdmission share - the only
// real difference between the two callers is which governance signal
// (live GovernanceTrust vs accumulated GovernanceConviction) feeds the
// 60% term. Callers are responsible for their own VotingPower==0 check
// beforehand (both already do), so this never re-derives wallet from
// address to avoid a redundant lookup.
func votingWeightUsing(address string, governanceTerm uint64) uint64 {
	staked := stakingStore.Stakes[address]["CCT"]
	normalizedStake := staked * 100 / core.StakeCap
	if normalizedStake > 100 {
		normalizedStake = 100
	}

	base := 1 + normalizedStake*40/100 + governanceTerm*60/100

	referralBonus := core.ReferralCount(walletStore.Wallets, address)
	if referralBonus > referralVotingBonusCap {
		referralBonus = referralVotingBonusCap
	}

	return base + referralBonus
}

// governanceTotalWeight sums votingWeight across every activated-
// governance wallet (VotingPower > 0) - bounded by real activated
// participants, not O(all wallets), same cost class as
// governanceFactorFor's existing sweep (api/borrowing_power.go). Used
// by the validator staged-onboarding pipeline (see
// api/validator_application.go) to compute the "1% of total
// governance weight" pre-vote threshold - this is the only place in
// the codebase that needed a network-wide total rather than one
// wallet's own weight, so it lives here next to votingWeight itself.
func governanceTotalWeight() uint64 {
	var total uint64
	for address, wallet := range walletStore.Wallets {
		if wallet.VotingPower > 0 {
			total += votingWeight(address)
		}
	}
	return total
}

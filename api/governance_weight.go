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

	staked := stakingStore.Stakes[address]["CCT"]
	normalizedStake := staked * 100 / core.StakeCap
	if normalizedStake > 100 {
		normalizedStake = 100
	}

	base := 1 + normalizedStake*40/100 + wallet.Reputation*60/100

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

package core

// reputationTierReward is the one-time REPT lump sum minted the FIRST
// time a wallet's reputation ever climbs into a given tier - reward
// scale loosely mirrors BorrowLimit's tier escalation, so the token
// that represents "reputation earned value" tracks the same
// progression curve the borrowing perks already use.
var reputationTierReward = map[CreditTier]uint64{
	TierSilver:  50,
	TierGold:    200,
	TierDiamond: 750,
	TierGenesis: 2500,
}

// allTiersInRankOrder is a fixed order to iterate over when summing
// the reward for a multi-tier jump - Reputation deltas are small
// (loan repayment: +10), so a single change crossing more than one
// tier boundary at once is unlikely, but should still pay out the
// full cumulative reward rather than only the destination tier's,
// exactly as if each tier had been crossed one at a time.
var allTiersInRankOrder = []CreditTier{TierBronze, TierSilver, TierGold, TierDiamond, TierGenesis}

// cumulativeTierReward sums the reward for every tier strictly above
// from's rank, up to and including to's rank - so a wallet that never
// reached Silver before and jumps straight to Gold still gets the
// Silver reward plus the Gold reward, not just Gold's.
func cumulativeTierReward(from, to CreditTier) uint64 {
	var total uint64
	fromRank := TierRank(from)
	toRank := TierRank(to)

	for _, tier := range allTiersInRankOrder {
		rank := TierRank(tier)
		if rank > fromRank && rank <= toRank {
			total += reputationTierReward[tier]
		}
	}

	return total
}

// TierReward pairs a tier with its one-time REPT reward.
type TierReward struct {
	Tier   CreditTier `json:"tier"`
	Reward uint64     `json:"reptReward"`
}

// TierRewards returns the one-time REPT reward for every tier that
// has one (Bronze doesn't - it's the starting tier, not something
// climbed into), in ascending-rank order - for anywhere that needs to
// *display* the reward table (e.g. the reputation-math explainer)
// rather than just apply it.
func TierRewards() []TierReward {
	out := make([]TierReward, 0, len(allTiersInRankOrder))
	for _, tier := range allTiersInRankOrder {
		if reward, ok := reputationTierReward[tier]; ok {
			out = append(out, TierReward{tier, reward})
		}
	}
	return out
}

// NextUnearnedTierReward reports the next tier still ahead of
// highestReached and the REPT reward reaching it will mint - what a
// wallet's reputation page shows as "what's next." ok is false once
// every tier has already been reached (Genesis is the ceiling).
func NextUnearnedTierReward(highestReached CreditTier) (next CreditTier, reward uint64, ok bool) {
	currentRank := TierRank(highestReached)

	for _, tier := range allTiersInRankOrder {
		if TierRank(tier) == currentRank+1 {
			return tier, reputationTierReward[tier], true
		}
	}

	return "", 0, false
}

package core

// CreditTier is a PRESENTATION-ONLY label derived from Wallet.Reputation.
// It is not a second reputation system - per the CreditChain "one owner
// per concept" rule, Wallet.Reputation is the sole canonical trust score.
// Tiers exist purely to make that single score visible and meaningful
// to users (larger loans, lower interest, governance weight, validator
// preference are all gated off the tier, which is itself just a
// labelled range over Wallet.Reputation).
type CreditTier string

const (
	TierBronze  CreditTier = "Bronze"
	TierSilver  CreditTier = "Silver"
	TierGold    CreditTier = "Gold"
	TierDiamond CreditTier = "Diamond"
	TierGenesis CreditTier = "Genesis"
)

// TierThreshold pairs a tier with the minimum reputation score that
// reaches it.
type TierThreshold struct {
	Tier   CreditTier `json:"tier"`
	MinRep uint64     `json:"minReputation"`
}

// tierThresholdsDesc is the single source of truth for tier
// breakpoints, highest-first so TierForReputation can short-circuit
// on the first match. Both TierForReputation and TierThresholds (used
// by anything that needs to *display* the breakpoints, e.g. the
// reputation-math explainer) read from this one table instead of
// keeping the numbers in two places that could drift apart.
// Breakpoints are governance-adjustable in principle; these are the
// bootstrap defaults.
var tierThresholdsDesc = []TierThreshold{
	{TierGenesis, 95},
	{TierDiamond, 80},
	{TierGold, 60},
	{TierSilver, 40},
	{TierBronze, 0},
}

// TierForReputation maps a Wallet.Reputation score (1-100) onto a
// CreditTier.
func TierForReputation(reputation uint64) CreditTier {
	for _, t := range tierThresholdsDesc {
		if reputation >= t.MinRep {
			return t.Tier
		}
	}
	return TierBronze
}

// TierThresholds returns the tier breakpoints in ascending-rank order
// (Bronze first, Genesis last).
func TierThresholds() []TierThreshold {
	out := make([]TierThreshold, len(tierThresholdsDesc))
	for i, t := range tierThresholdsDesc {
		out[len(tierThresholdsDesc)-1-i] = t
	}
	return out
}

// TierRank gives CreditTier a total order (Bronze lowest, Genesis
// highest) - used to tell "climbed to a higher tier" apart from
// "moved to a different tier" (a drop should never be treated as
// progress, e.g. by ReputationTierReward).
func TierRank(tier CreditTier) int {
	switch tier {
	case TierSilver:
		return 1
	case TierGold:
		return 2
	case TierDiamond:
		return 3
	case TierGenesis:
		return 4
	default: // TierBronze, or an unset/unrecognized value
		return 0
	}
}

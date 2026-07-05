package core

// InterestRate is keyed off Wallet.Reputation (1-100 scale), aligned
// with the CreditTier breakpoints - higher tiers unlock lower rates,
// per the "Credit Tiers unlock lower interest" requirement.
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

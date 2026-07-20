package core

// BorrowLimit is keyed off Wallet.Reputation (1-100 scale), aligned
// with the CreditTier breakpoints in credit_score.go so a user's
// borrowing power always matches the tier they see in the UI.
func BorrowLimit(reputation uint64) uint64 {
	switch TierForReputation(reputation) {
	case TierGenesis:
		return 100000
	case TierDiamond:
		return 50000
	case TierGold:
		return 25000
	case TierSilver:
		return 10000
	default: // TierBronze
		return 1000
	}
}

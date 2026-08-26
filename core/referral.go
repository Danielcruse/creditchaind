package core

// ReferralCount reports how many wallets list address as their
// ReferredBy - derived live from the wallet map, never stored
// separately (same "derived, never stored" convention as CreditTier/
// Mining Power), so it's always exactly consistent with the
// underlying Wallet.ReferredBy fields. O(all wallets) - a known,
// disclosed scaling limit shared with every other full-wallet-map
// sweep already in this codebase (dormancy, reputation mining), not
// solved here.
func ReferralCount(wallets map[string]*Wallet, address string) uint64 {
	var count uint64
	for _, w := range wallets {
		if w.ReferredBy == address {
			count++
		}
	}
	return count
}

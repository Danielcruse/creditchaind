package core

// SyncWallet updates w.StakedETH/StakedCCT/StakedUSDCTEST to match the
// canonical StakingStore ledger for the given address. These fields
// are a read-model only - nothing should increment them directly.
func (s *StakingStore) SyncWallet(w *Wallet, address string) {
	w.StakedETH = s.Stakes[address]["ETHTEST"]
	w.StakedCCT = s.Stakes[address]["CCT"]
	w.StakedUSDCTEST = s.Stakes[address]["USDCTEST"]
}

// SyncValidator updates v.Stake to match the canonical StakingStore
// ledger for the validator's own wallet address. CCT-only (not the
// sum of all staked tokens) - PoSR's "Stake the minimum required
// amount of CCT" ties validator eligibility and selection weight
// specifically to CCT, matching api/consensus_loop.go's
// hydrateValidator (the actual production-path equivalent of this
// sync). Validator.Stake is a read-model only, exactly like Wallet's
// Staked* fields.
func (s *StakingStore) SyncValidator(v *Validator) {
	v.Stake = s.Stakes[v.Wallet.Address]["CCT"]
}

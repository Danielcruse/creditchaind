package core

// SyncWallet updates w.StakedETH/StakedCCT/StakedUSDCTEST to match the
// canonical StakingStore ledger for the given address. These fields
// are a read-model only - nothing should increment them directly.
//
// Staked CCT deliberately stays on the original Stakes map (uint64,
// whole units), NOT the CctStore/CCTStakes big-int machinery the
// 18-decimal migration introduced for CCT's general ledger/AMM/order-
// book paths - MaxTestStakePerAccount (10,000) bounds any single
// account's stake far below where uint64 precision could ever matter,
// so staking accounting doesn't need it. Only the real escrow
// TRANSFER of value in/out of "STAKE:CCT" (api/apply.go's applyStake/
// applyUnstake/slashStake) goes through cctStore, converting at the
// boundary via CctWholeToSmallest/CctSmallestToWholeFloor - the
// accounting ledger itself (this map) is unaffected.
func (s *StakingStore) SyncWallet(w *Wallet, address string) {
	// StakedETH sums ETHTEST (legacy, no longer stakeable going
	// forward - see api/apply.go's applyStake) and ETHB (the real
	// bridged/custodial ETH that replaced it as the stakeable "ETH"
	// asset) so this field keeps meaning "how much ETH-equivalent is
	// this wallet staking" through the migration window, rather than
	// silently dropping a wallet's still-outstanding legacy position
	// from view the moment ETHB staking ships. api/staking.go's
	// LegacyPositions field breaks the two back out separately for any
	// caller that needs to distinguish them.
	w.StakedETH = s.Stakes[address]["ETHTEST"] + s.Stakes[address]["ETHB"]
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
// Staked* fields - see SyncWallet's doc comment for why this stays on
// the original whole-unit Stakes map.
func (s *StakingStore) SyncValidator(v *Validator) {
	v.Stake = s.Stakes[v.Wallet.Address]["CCT"]
}

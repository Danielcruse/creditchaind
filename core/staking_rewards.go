package core

func (s *StakingStore) StakeETH(
	address string,
	symbol string,
	amount uint64,
) {
	if s.Stakes[address] == nil {
		s.Stakes[address] = make(map[string]uint64)
	}
	s.Stakes[address][symbol] += amount
}

// Slash seizes up to amount of address's staked symbol, clamped at
// whatever's actually still staked (a validator already partially
// slashed may have less than the nominal penalty), and returns the
// amount actually seized. Callers are responsible for burning that
// amount from the matching escrow balance (see api/apply.go's
// stakeEscrowAddress/applySlashReport) - this only updates the
// canonical stake ledger.
func (s *StakingStore) Slash(
	address string,
	symbol string,
	amount uint64,
) uint64 {
	staked := s.Stakes[address][symbol]
	if amount > staked {
		amount = staked
	}
	s.Stakes[address][symbol] = staked - amount
	return amount
}

// Reward is a live-computed display figure (10% of address's current
// total symbol stake), not a cumulative earned-rewards tracker - see
// api/apply.go's applyStake for what actually gets minted on a stake
// action (based on the amount just staked, not this total).
func (s *StakingStore) Reward(
	address string,
	symbol string,
) uint64 {

	return RewardForStake(
		s.Stakes[address][symbol],
	)
}

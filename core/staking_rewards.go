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

package core

func (s *StakingStore) StakeETH(
	address string,
	amount uint64,
) {
	s.Stakes[address] += amount
}

func (s *StakingStore) Reward(
	address string,
) uint64 {

	return RewardForStake(
		s.Stakes[address],
	)
}

package core

type UserStake struct {
	Address string
	ETH     uint64
	CCT     uint64
	Points  uint64
}

func NewUserStake(
	address string,
) *UserStake {
	return &UserStake{
		Address: address,
	}
}

func (s *UserStake) StakeETH(
	amount uint64,
) {
	s.ETH += amount
	s.Points += amount * 10
}

func (s *UserStake) StakeCCT(
	amount uint64,
) {
	s.CCT += amount
	s.Points += amount
}
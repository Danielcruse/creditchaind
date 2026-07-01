package core

type UserStakeStore struct {
	Stakes map[string]*UserStake
}

func NewUserStakeStore() *UserStakeStore {
	return &UserStakeStore{
		Stakes: make(
			map[string]*UserStake,
		),
	}
}

func (ss *UserStakeStore) Get(
	address string,
) *UserStake {

	stake, ok := ss.Stakes[address]

	if !ok {
		stake = NewUserStake(address)
		ss.Stakes[address] = stake
	}

	return stake
}
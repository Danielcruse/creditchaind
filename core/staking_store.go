package core

type StakingStore struct {
	Stakes map[string]uint64
}

func NewStakingStore() *StakingStore {
	return &StakingStore{
		Stakes: make(map[string]uint64),
	}
}
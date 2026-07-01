package core

type CollateralStore struct {
	Locked map[string]uint64
}

func NewCollateralStore() *CollateralStore {
	return &CollateralStore{
		Locked: make(map[string]uint64),
	}
}
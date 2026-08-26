package core

// Locked is address -> symbol -> amount, so a user's locked CCT and
// locked oETH are tracked independently rather than commingled into
// one undifferentiated number.
type CollateralStore struct {
	Locked map[string]map[string]uint64
}

func NewCollateralStore() *CollateralStore {
	return &CollateralStore{
		Locked: make(map[string]map[string]uint64),
	}
}

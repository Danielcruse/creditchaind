package core

type LenderStats struct {
	Address     string
	LoansFunded uint64
	AmountLent  uint64
	InterestEarned uint64
}

type LenderStore struct {
	Stats map[string]*LenderStats
}

func NewLenderStore() *LenderStore {
	return &LenderStore{
		Stats: make(
			map[string]*LenderStats,
		),
	}
}

func (l *LenderStore) Get(
	address string,
) *LenderStats {

	s, ok :=
		l.Stats[address]

	if ok {
		return s
	}

	s = &LenderStats{
		Address: address,
	}

	l.Stats[address] = s

	return s
}
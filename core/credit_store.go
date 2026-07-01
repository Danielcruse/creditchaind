package core

type CreditStore struct {
	Profiles map[string]*CreditProfile
}

func NewCreditStore() *CreditStore {
	return &CreditStore{
		Profiles: make(
			map[string]*CreditProfile,
		),
	}
}

func (c *CreditStore) Get(
	address string,
) *CreditProfile {

	p, ok := c.Profiles[address]

	if ok {
		return p
	}

	p = &CreditProfile{
		Address: address,
		Score:   500,
	}

	c.Profiles[address] = p

	return p
}
func (c *CreditStore) UpdateReputation(
	address string,
	amount uint64,
) {
	p := c.Get(address)

	p.Reputation += amount

	CalculateScore(p)
}
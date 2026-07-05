package core

// CreditProfile holds loan-history counters for an address. It does
// NOT store a reputation or score of its own - Wallet.Reputation is
// the single canonical trust score. Use TierForReputation alongside
// a wallet lookup to get the presentation-layer tier for an address.
type CreditProfile struct {
	Address        string
	LoansTaken     uint64
	LoansRepaid    uint64
	LoansDefaulted uint64
}

type CreditStore struct {
	Profiles map[string]*CreditProfile
}

func NewCreditStore() *CreditStore {
	return &CreditStore{
		Profiles: make(map[string]*CreditProfile),
	}
}

func (c *CreditStore) Get(address string) *CreditProfile {
	p, ok := c.Profiles[address]

	if ok {
		return p
	}

	p = &CreditProfile{
		Address: address,
	}

	c.Profiles[address] = p

	return p
}

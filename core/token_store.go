package core

type TokenStore struct {
	Balances map[string]uint64
	Token    Token
}

func NewTokenStore() *TokenStore {
	return &TokenStore{
		Balances: make(map[string]uint64),
		Token: Token{
			Name:        "CreditChain Token",
			Symbol:      "CCT",
			TotalSupply: 0,
		},
	}
}
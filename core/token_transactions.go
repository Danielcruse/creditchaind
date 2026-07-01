package core

func (t *TokenStore) Mint(
	address string,
	amount uint64,
) {
	t.Balances[address] += amount
	t.Token.TotalSupply += amount
}

func (t *TokenStore) Transfer(
	from string,
	to string,
	amount uint64,
) bool {

	if t.Balances[from] < amount {
		return false
	}

	t.Balances[from] -= amount
	t.Balances[to] += amount

	return true
}

func (t *TokenStore) BalanceOf(
	address string,
) uint64 {
	return t.Balances[address]
}
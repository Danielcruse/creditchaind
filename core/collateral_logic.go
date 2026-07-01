package core

func (c *CollateralStore) Lock(
	address string,
	amount uint64,
	token *TokenStore,
) bool {

	if token.BalanceOf(address) < amount {
		return false
	}

	token.Balances[address] -= amount
	c.Locked[address] += amount

	return true
}

func (c *CollateralStore) Unlock(
	address string,
	amount uint64,
	token *TokenStore,
) bool {

	if c.Locked[address] < amount {
		return false
	}

	c.Locked[address] -= amount
	token.Balances[address] += amount

	return true
}
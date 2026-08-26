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

// Burn is Mint's exact inverse - removes amount from address AND from
// total supply, rather than sending it anywhere. Used for repaying a
// self-funded loan (see api/apply.go's applyRepay), which minted new
// supply out of thin air with no real lender behind it - repayment
// undoes that inflation instead of crediting a synthetic escrow
// address nobody actually lent from.
func (t *TokenStore) Burn(
	address string,
	amount uint64,
) bool {
	if t.Balances[address] < amount {
		return false
	}

	t.Balances[address] -= amount
	t.Token.TotalSupply -= amount

	return true
}
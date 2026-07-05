package core

// Add records amount as newly locked collateral for address under
// symbol - pure bookkeeping. The caller (api/apply.go's
// applyLockCollateral) is responsible for having already moved the
// real balance (staked CCT or oETH) out of its source before calling
// this, the same "move first, then record" order every other escrow
// bucket in this codebase already follows.
func (c *CollateralStore) Add(address, symbol string, amount uint64) {
	if c.Locked[address] == nil {
		c.Locked[address] = make(map[string]uint64)
	}
	c.Locked[address][symbol] += amount
}

// Release reverses Add - it only succeeds if that much of that symbol
// is actually locked, and the caller is responsible for moving the
// real balance back to its source (staked CCT or oETH) once this
// returns true.
func (c *CollateralStore) Release(address, symbol string, amount uint64) bool {
	if c.Locked[address][symbol] < amount {
		return false
	}

	c.Locked[address][symbol] -= amount
	return true
}

// LockedOf is a nil-safe read of how much of symbol address currently
// has locked as collateral.
func (c *CollateralStore) LockedOf(address, symbol string) uint64 {
	return c.Locked[address][symbol]
}

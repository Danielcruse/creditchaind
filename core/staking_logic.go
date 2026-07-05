package core

// UnbondingPeriodSeconds is how long a queued unstake stays bonded
// (still weighted, still slashable) before it actually leaves Stakes.
// Demo-scale value - a real deployment would set this to days, not
// seconds, matching how every real PoS chain does it (Cosmos ~21
// days, Ethereum's exit queue, etc.). Short here so the whole
// lifecycle can actually be exercised/verified in one sitting.
const UnbondingPeriodSeconds = 60

// MaxTestStakePerAccount caps how much of any one token (CCT, ETHTEST,
// or USDCTEST - checked independently per symbol) a single account
// can have staked at once. Purely a shared-pilot fairness cap, not an
// economic security mechanism - there's no real value at stake yet.
const MaxTestStakePerAccount = 10_000

// UnbondingTotal is how much of address's symbol stake is already
// queued to unbond - needed so a second unstake request can't
// double-count funds that are already on their way out.
func (s *StakingStore) UnbondingTotal(address, symbol string) uint64 {
	var total uint64
	for _, e := range s.Unbonding {
		if e.Address == address && e.Symbol == symbol {
			total += e.Amount
		}
	}
	return total
}

// UnbondingFor returns ALL of address's pending unbonding entries,
// across every symbol - for display purposes, where the caller can
// group/filter by each entry's own Symbol.
func (s *StakingStore) UnbondingFor(address string) []UnbondingEntry {
	entries := []UnbondingEntry{}
	for _, e := range s.Unbonding {
		if e.Address == address {
			entries = append(entries, e)
		}
	}
	return entries
}

// AvailableToUnbond is address's symbol stake not already queued to
// unbond.
func (s *StakingStore) AvailableToUnbond(address, symbol string) uint64 {
	staked := s.Stakes[address][symbol]
	queued := s.UnbondingTotal(address, symbol)
	if queued >= staked {
		return 0
	}
	return staked - queued
}

// BeginUnbond queues an unstake request. It does NOT remove the stake
// yet - see the package doc on UnbondingEntry for why. creditAddress
// is captured now (not re-derived at maturity) since ETHTEST/USDCTEST
// sub-addresses are derived from the staker's public key, which isn't
// available at the point ProcessMaturedUnbonds runs.
func (s *StakingStore) BeginUnbond(address, symbol string, amount uint64, maturesAt int64, creditAddress string) bool {
	if s.AvailableToUnbond(address, symbol) < amount {
		return false
	}

	s.Unbonding = append(s.Unbonding, UnbondingEntry{
		Address:       address,
		Symbol:        symbol,
		CreditAddress: creditAddress,
		Amount:        amount,
		MaturesAt:     maturesAt,
	})

	return true
}

// ProcessMaturedUnbonds removes stake for every entry whose cooldown
// has elapsed as of asOf, and returns exactly those matured entries so
// the caller can actually pay each one out. Deliberately driven by a
// BLOCK's own timestamp (see api/apply.go's ApplyBlock), not the
// calling node's wall clock - every node processes maturity
// identically when replaying the same block, preserving the same
// convergence guarantee every other state change in this codebase
// relies on.
//
// This function only touches the Stakes ledger - it deliberately does
// NOT credit any balance itself. core has no access to the token
// stores that own CCT/ETHTEST/USDCTEST balances (those live in the api
// package, which imports core, not the other way around), so paying
// out a matured entry is the caller's job.
func (s *StakingStore) ProcessMaturedUnbonds(asOf int64) []UnbondingEntry {
	matured := make([]UnbondingEntry, 0)
	remaining := make([]UnbondingEntry, 0, len(s.Unbonding))

	for _, e := range s.Unbonding {
		if asOf >= e.MaturesAt {
			if s.Stakes[e.Address] == nil {
				s.Stakes[e.Address] = make(map[string]uint64)
			}
			if s.Stakes[e.Address][e.Symbol] >= e.Amount {
				s.Stakes[e.Address][e.Symbol] -= e.Amount
			} else {
				s.Stakes[e.Address][e.Symbol] = 0
			}
			matured = append(matured, e)
			continue
		}
		remaining = append(remaining, e)
	}

	s.Unbonding = remaining
	return matured
}

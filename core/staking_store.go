package core

// UnbondingEntry is a queued, not-yet-final unstake request. The
// amount stays counted in Stakes (still bonded, still at risk of
// slashing, still contributing to PoSR weight) until MaturesAt -
// matching how real PoS systems handle unbonding: you can't just
// unstake and instantly walk away from consensus responsibilities.
type UnbondingEntry struct {
	Address string
	// Symbol is which token this entry unbonds - "CCT", "ETHTEST", or
	// "USDCTEST". Entries persisted before multi-token staking existed
	// decode with this at the Go zero value (""); callers treat that
	// as "ETHTEST", the only token stakeable before this field existed.
	Symbol string
	// CreditAddress is where the matured amount actually gets paid out
	// - the raw wallet address for CCT, or the ETHTEST/USDCTEST
	// sub-address derived from the staker's public key at the time the
	// unstake was requested (sub-addresses can't be re-derived later
	// from Address alone - see core/address.go). Entries persisted
	// before this field existed fall back to Address itself.
	CreditAddress string
	Amount        uint64
	MaturesAt     int64 // unix seconds - a BLOCK timestamp, not wall-clock (see ProcessMaturedUnbonds)
}

// Stakes is address -> symbol -> amount, so one account can have
// independent CCT, ETHTEST, and USDCTEST positions at once rather than
// a single undifferentiated number.
type StakingStore struct {
	Stakes    map[string]map[string]uint64
	Unbonding []UnbondingEntry
}

func NewStakingStore() *StakingStore {
	return &StakingStore{
		Stakes:    make(map[string]map[string]uint64),
		Unbonding: []UnbondingEntry{},
	}
}

// StakeOf is a nil-safe read of how much of symbol address currently
// has staked - reading through a missing outer or inner map key is
// safe in Go (returns the zero value), so this is really just a
// readable shorthand, not a nil-guard.
func (s *StakingStore) StakeOf(address, symbol string) uint64 {
	return s.Stakes[address][symbol]
}

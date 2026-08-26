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
// independent CCT/ETHTEST/USDCTEST positions at once rather than a
// single undifferentiated number. CCT staking accounting deliberately
// stays here (plain uint64, whole units) even after the 18-decimal
// migration gave CCT's general ledger big-int precision - see
// core/staking_sync.go's doc comment for why (MaxTestStakePerAccount
// bounds any account's stake far below where that would matter). Only
// the real escrow balance backing a CCT stake moved to the new
// cctStore (api/apply.go's applyStake/applyUnstake/slashStake
// convert at the boundary via CctWholeToSmallest).
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

// ReduceStakeDirect removes amount of symbol from address's bonded
// stake immediately, with no unbonding entry queued - unlike a normal
// unstake (BeginUnbond), this is for the legacy-stake migration sweep
// (api/legacy_stake_migration.go), an involuntary, protocol-initiated
// closure rather than a user withdrawal request, so the unbonding
// period's anti-flash-unstake rationale doesn't apply. Bounds-checked:
// returns false (no-op) if address doesn't have at least amount of
// symbol staked, matching every other fail-closed mutator in this file.
func (s *StakingStore) ReduceStakeDirect(address, symbol string, amount uint64) bool {
	if s.Stakes[address][symbol] < amount {
		return false
	}
	s.Stakes[address][symbol] -= amount
	return true
}

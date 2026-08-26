package core

// StakePurposeStore tracks how much of an address's staked CCT is
// earmarked for a specific purpose - moderation stake, Trading Hub
// stake-vault, or validator-pool join stake - as a separate concern
// from StakingStore's own address -> symbol -> amount ledger (which
// only knows "how much is staked," not "why"). Mirrors
// CollateralStore's exact shape (core/collateral_store.go) and
// Add/Release/LockedOf method set on purpose, since it's the same
// "how much of a fixed total is earmarked right now" bookkeeping
// problem, just with a purpose string in place of a token symbol.
//
// This store does no bounds-checking against the address's actual
// total staked CCT (stakingStore.Stakes, an api-package var this core
// type has no access to) - callers (Phase 4's hub/moderator
// registration and validator-pool-join handlers) are responsible for
// checking that a new lock wouldn't push total-locked-across-every-
// purpose past what's actually staked, the same "caller checks the
// real balance before recording the bookkeeping" convention
// CollateralStore.Add already follows.
type StakePurposeStore struct {
	Locked map[string]map[string]uint64
}

// Purpose values a lock can be tagged with.
const (
	StakePurposeModeration    = "MODERATION"
	StakePurposeHub           = "HUB"
	StakePurposeValidatorPool = "VALIDATOR_POOL"
)

func NewStakePurposeStore() *StakePurposeStore {
	return &StakePurposeStore{
		Locked: make(map[string]map[string]uint64),
	}
}

// Lock records amount as newly locked for address under purpose -
// pure bookkeeping, same "move/verify first, then record" order
// CollateralStore.Add's callers already follow.
func (s *StakePurposeStore) Lock(address, purpose string, amount uint64) {
	if s.Locked[address] == nil {
		s.Locked[address] = make(map[string]uint64)
	}
	s.Locked[address][purpose] += amount
}

// Release reverses Lock - only succeeds if that much of that purpose
// is actually locked for address.
func (s *StakePurposeStore) Release(address, purpose string, amount uint64) bool {
	if s.Locked[address][purpose] < amount {
		return false
	}

	s.Locked[address][purpose] -= amount
	return true
}

// LockedOf is a nil-safe read of how much address currently has
// locked for purpose.
func (s *StakePurposeStore) LockedOf(address, purpose string) uint64 {
	return s.Locked[address][purpose]
}

// TotalLocked sums every purpose's lock for address - what a caller
// checks against the address's real total staked CCT before allowing
// a new Lock call to proceed (staked-but-already-earmarked-elsewhere
// CCT can't be double-locked for a second purpose).
func (s *StakePurposeStore) TotalLocked(address string) uint64 {
	var total uint64
	for _, amount := range s.Locked[address] {
		total += amount
	}
	return total
}

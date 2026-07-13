package core

// DelegationUnbondingEntry mirrors UnbondingEntry (see staking_store.go)
// for delegated CCT - same "still counted, still contributing to the
// validator's PoSR weight, until MaturesAt" semantics as a regular
// stake unbond.
type DelegationUnbondingEntry struct {
	Validator string
	Delegator string
	Amount    uint64
	MaturesAt int64 // unix seconds - a BLOCK timestamp, not wall-clock, same convention as UnbondingEntry
}

// DelegationStore is the canonical ledger of who has delegated how
// much CCT to which validator - a third party backing a validator's
// PoSR weight without themselves gaining any block-production rights
// (see ValidatorStatus.AuthorizedNodeID, which is a completely
// separate concept: which node produces blocks, never whose funds
// back the validator). Keyed by validator address, then by delegator
// address, so one delegator's positions across multiple validators
// are independent of each other.
type DelegationStore struct {
	// Delegations is validator -> delegator -> amount.
	Delegations map[string]map[string]uint64
	Unbonding   []DelegationUnbondingEntry
}

func NewDelegationStore() *DelegationStore {
	return &DelegationStore{
		Delegations: make(map[string]map[string]uint64),
		Unbonding:   []DelegationUnbondingEntry{},
	}
}

// DelegatedBy is how much delegator has currently delegated to
// validator (still counted even while an undelegate is queued/
// unbonding - see AvailableToUndelegate for the spendable-for-a-new-
// undelegate view).
func (d *DelegationStore) DelegatedBy(validator, delegator string) uint64 {
	return d.Delegations[validator][delegator]
}

// TotalDelegatedTo is validator's total delegated CCT across every
// delegator - fed into PoSR weighting (see api/consensus_loop.go's
// hydrateValidator) and into applyBlockReward's pro-rata split.
func (d *DelegationStore) TotalDelegatedTo(validator string) uint64 {
	var total uint64
	for _, amount := range d.Delegations[validator] {
		total += amount
	}
	return total
}

// DelegatorCount is how many distinct addresses currently back
// validator - 0 if nobody has, including if validator was never
// looked up before (nil map read is safe in Go).
func (d *DelegationStore) DelegatorCount(validator string) int {
	return len(d.Delegations[validator])
}

// Delegate records amount more CCT backing validator from delegator -
// the caller (applyValidatorDelegate) has already moved the real CCT
// into escrow before calling this; this only updates the ledger.
func (d *DelegationStore) Delegate(validator, delegator string, amount uint64) {
	if d.Delegations[validator] == nil {
		d.Delegations[validator] = make(map[string]uint64)
	}
	d.Delegations[validator][delegator] += amount
}

// UnbondingTotal is how much of delegator's position with validator is
// already queued to undelegate - needed so a second undelegate request
// can't double-count funds already on their way out, same reasoning as
// StakingStore.UnbondingTotal.
func (d *DelegationStore) UnbondingTotal(validator, delegator string) uint64 {
	var total uint64
	for _, e := range d.Unbonding {
		if e.Validator == validator && e.Delegator == delegator {
			total += e.Amount
		}
	}
	return total
}

// AvailableToUndelegate is delegator's position with validator not
// already queued to undelegate.
func (d *DelegationStore) AvailableToUndelegate(validator, delegator string) uint64 {
	delegated := d.Delegations[validator][delegator]
	queued := d.UnbondingTotal(validator, delegator)
	if queued >= delegated {
		return 0
	}
	return delegated - queued
}

// BeginUndelegate queues an undelegate request - does NOT remove the
// delegation yet (still counts toward the validator's PoSR weight and
// still shares in rewards until it matures), same reasoning as
// StakingStore.BeginUnbond.
func (d *DelegationStore) BeginUndelegate(validator, delegator string, amount uint64, maturesAt int64) bool {
	if d.AvailableToUndelegate(validator, delegator) < amount {
		return false
	}

	d.Unbonding = append(d.Unbonding, DelegationUnbondingEntry{
		Validator: validator,
		Delegator: delegator,
		Amount:    amount,
		MaturesAt: maturesAt,
	})

	return true
}

// ProcessMaturedUndelegations removes the ledger entry for every
// undelegate whose cooldown has elapsed as of asOf, and returns
// exactly those matured entries so the caller can pay each one out -
// mirrors StakingStore.ProcessMaturedUnbonds exactly, including being
// driven by a BLOCK's own timestamp for cross-node determinism.
func (d *DelegationStore) ProcessMaturedUndelegations(asOf int64) []DelegationUnbondingEntry {
	matured := make([]DelegationUnbondingEntry, 0)
	remaining := make([]DelegationUnbondingEntry, 0, len(d.Unbonding))

	for _, e := range d.Unbonding {
		if asOf >= e.MaturesAt {
			if d.Delegations[e.Validator] != nil {
				if d.Delegations[e.Validator][e.Delegator] >= e.Amount {
					d.Delegations[e.Validator][e.Delegator] -= e.Amount
				} else {
					d.Delegations[e.Validator][e.Delegator] = 0
				}
			}
			matured = append(matured, e)
			continue
		}
		remaining = append(remaining, e)
	}

	d.Unbonding = remaining
	return matured
}

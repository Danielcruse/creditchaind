package core

import "sort"

// ValidatorStatus is a lightweight, shared record of an address's
// validator registration. Stake and Reputation are deliberately not
// duplicated here - callers read those from StakingStore/WalletStore
// (the canonical owners) and only use this for registration state and
// block-production counters.
type ValidatorStatus struct {
	Address        string
	Active         bool
	BlocksProduced uint64
	MissedBlocks   uint64
	SlashCount     uint64

	// PermanentlyBanned is set once SlashCount crosses
	// PermanentBanSlashCountThreshold (see api/apply.go's
	// applySlashReport) - PoSR's "no unresolved validator penalties"
	// and "repeated offences may permanently remove validator
	// privileges." No appeal path exists, consistent with how
	// slashing itself is already a single-report-trusted mechanism
	// with no dispute process.
	PermanentlyBanned bool

	// AuthorizedNodeID/AuthorizedNodePublicKey identify the ONE node
	// this validator's key-holder actually delegated block-production
	// to (implicitly, by registering through it). Every other node -
	// even one that knows this validator exists via replicated state -
	// must not produce on its behalf. Set once per registration
	// transaction, replicated identically everywhere (see
	// api/apply.go's applyValidatorRegister).
	AuthorizedNodeID        string
	AuthorizedNodePublicKey string

	// CommissionBps is this validator's cut of pool block rewards, in
	// basis points (100 = 1%), taken off the top before the remainder
	// splits pro-rata across delegators (see api/apply.go's
	// applyBlockReward) - completely separate from AuthorizedNodeID
	// above: a delegator only ever backs this validator's PoSR weight
	// and shares its rewards, never gains any say in which node
	// produces blocks. Only meaningful once this validator actually has
	// delegators (TotalDelegatedTo > 0) - a validator with none keeps
	// 100% of its own block rewards regardless of this value, same as
	// before delegation existed at all.
	CommissionBps uint16
}

// DefaultCommissionBps is what a validator starts with on first
// registration (10%) - a reasonable default a delegator would expect,
// not free (0%, no incentive to ever change it) and not confiscatory
// (100%, no reason for anyone to ever delegate). Only applied once, at
// creation - re-registering an already-known validator (see Register
// below) never resets a commission rate they've since changed.
const DefaultCommissionBps = 1000

// MaxCommissionBps caps what a validator may ever set their own
// commission to (50%) - prevents a validator from setting a rate so
// high delegating to them would be pointless, without banning a
// meaningfully high rate outright.
const MaxCommissionBps = 5000

// Uptime is BlocksProduced/(BlocksProduced+MissedBlocks), defaulting
// to a clean 1.0 before this validator has had any rounds at all -
// fed into consensus.go's PoSR weighting.
func (v *ValidatorStatus) Uptime() float64 {
	total := v.BlocksProduced + v.MissedBlocks
	if total == 0 {
		return 1.0
	}
	return float64(v.BlocksProduced) / float64(total)
}

// ValidatorStore is the canonical, address-keyed registry of which
// wallets have registered as validators, shared by every client that
// hits this node.
type ValidatorStore struct {
	Validators map[string]*ValidatorStatus
}

func NewValidatorStore() *ValidatorStore {
	return &ValidatorStore{
		Validators: make(map[string]*ValidatorStatus),
	}
}

// Register (re-)authorizes address to produce blocks, delegating that
// authority to the specific node identified by nodeID/nodePublicKey.
// Only a transaction signed by address's own key can ever call this
// (see applyValidatorRegister), so re-delegating to a different node
// requires the validator's own key-holder to do it.
func (vs *ValidatorStore) Register(address, nodeID, nodePublicKey string) *ValidatorStatus {
	v, ok := vs.Validators[address]
	if ok {
		v.Active = true
		v.AuthorizedNodeID = nodeID
		v.AuthorizedNodePublicKey = nodePublicKey
		return v
	}

	v = &ValidatorStatus{
		Address:                 address,
		Active:                  true,
		AuthorizedNodeID:        nodeID,
		AuthorizedNodePublicKey: nodePublicKey,
		CommissionBps:           DefaultCommissionBps,
	}
	vs.Validators[address] = v

	return v
}

// SetCommission updates address's commission rate - only ever called
// from applyValidatorSetCommission, which independently re-verifies
// the caller actually IS address before this runs (never trusted from
// submission-time state alone). Silently no-ops on an unknown address
// or an out-of-range rate rather than erroring, matching this
// codebase's convention that apply-side functions never trust
// submission-time state and just decline to act on anything invalid.
func (vs *ValidatorStore) SetCommission(address string, bps uint16) bool {
	v, ok := vs.Validators[address]
	if !ok || bps > MaxCommissionBps {
		return false
	}
	v.CommissionBps = bps
	return true
}

func (vs *ValidatorStore) Get(address string) (*ValidatorStatus, bool) {
	v, ok := vs.Validators[address]
	return v, ok
}

// List returns every registered validator sorted by address - the
// underlying map iterates in randomized order, so without this sort
// two honest nodes holding byte-identical validator state could still
// build differently-ordered slices, and since selectFrom's weighted
// draw walks the pool in order, a different order can pick a
// different leader from the same previous-block hash (see audit
// finding C-02, KNOWN_GAPS.md). Sorting by address is an arbitrary but
// canonical key every node can independently reproduce.
func (vs *ValidatorStore) List() []*ValidatorStatus {
	list := make([]*ValidatorStatus, 0, len(vs.Validators))
	for _, v := range vs.Validators {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Address < list[j].Address
	})
	return list
}

// RecordBlock returns the validator's new BlocksProduced total (0 if
// address isn't a registered validator) - callers use this to trigger
// the "+1 reputation every N blocks" rule without needing a second
// lookup (see api/apply.go's applyBlockReward).
func (vs *ValidatorStore) RecordBlock(address string) uint64 {
	v, ok := vs.Validators[address]
	if !ok {
		return 0
	}

	v.BlocksProduced++
	return v.BlocksProduced
}

func (vs *ValidatorStore) RecordMiss(address string) {
	v, ok := vs.Validators[address]
	if !ok {
		return
	}

	v.MissedBlocks++
}

func (vs *ValidatorStore) RecordSlash(address string, strikes uint64) {
	v, ok := vs.Validators[address]
	if !ok {
		return
	}

	v.SlashCount += strikes
}

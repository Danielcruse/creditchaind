package core

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

	// AuthorizedNodeID/AuthorizedNodePublicKey identify the ONE node
	// this validator's key-holder actually delegated block-production
	// to (implicitly, by registering through it). Every other node -
	// even one that knows this validator exists via replicated state -
	// must not produce on its behalf. Set once per registration
	// transaction, replicated identically everywhere (see
	// api/apply.go's applyValidatorRegister).
	AuthorizedNodeID        string
	AuthorizedNodePublicKey string
}

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
	}
	vs.Validators[address] = v

	return v
}

func (vs *ValidatorStore) Get(address string) (*ValidatorStatus, bool) {
	v, ok := vs.Validators[address]
	return v, ok
}

func (vs *ValidatorStore) List() []*ValidatorStatus {
	list := make([]*ValidatorStatus, 0, len(vs.Validators))
	for _, v := range vs.Validators {
		list = append(list, v)
	}
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

package core

import (
	"sort"
	"sync"
)

// AdminMultisigStatus tracks a proposal's lifecycle - PENDING until
// enough of the network's configured signers approve it, EXECUTED
// exactly once after that (see api/admin_multisig.go's
// maybeExecuteAdminMultisigProposal for the single-execution guard).
type AdminMultisigStatus string

const (
	AdminMultisigPending  AdminMultisigStatus = "PENDING"
	AdminMultisigExecuted AdminMultisigStatus = "EXECUTED"
)

// AdminMultisigProposal is a pending admin action awaiting M-of-N
// approval from the network's configured multisig signers - this is
// what replaces the single ADMIN_WALLET_ADDRESS model: no single
// private key, however it's stored, can ever move funds or change
// protocol state alone once multisig is configured. ActionType is one
// of the existing admin-only TransactionTypes (TxTokenMint,
// TxGenesisPoolWithdraw, etc.) and Payload is the exact same JSON
// shape that type's own apply* function already expects in a
// transaction's Data field - multisig only gates WHEN that function
// runs, never changes what it does.
type AdminMultisigProposal struct {
	ID         string
	ActionType TransactionType
	Payload    string
	Proposer   string
	Approvals  map[string]bool
	Status     AdminMultisigStatus
	CreatedAt  int64
	ExecutedAt int64
}

// AdminMultisigStore is the shared, address-keyed registry of every
// admin action ever proposed - kept forever (not pruned), matching
// this codebase's existing convention of never deleting a record that
// serves as an audit trail (e.g. LoanOfferStore, ProposalStore).
type AdminMultisigStore struct {
	mu        sync.Mutex
	Proposals map[string]*AdminMultisigProposal
}

func NewAdminMultisigStore() *AdminMultisigStore {
	return &AdminMultisigStore{Proposals: make(map[string]*AdminMultisigProposal)}
}

func (s *AdminMultisigStore) Get(id string) (*AdminMultisigProposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Proposals[id]
	return p, ok
}

func (s *AdminMultisigStore) Create(p *AdminMultisigProposal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Proposals[p.ID] = p
}

// Pending returns every still-open proposal, sorted oldest-created
// first - deterministic ordering for display purposes (this is a
// read-only listing helper, not itself consensus-critical, but sorted
// output is still nicer for a UI than random map order).
func (s *AdminMultisigStore) Pending() []*AdminMultisigProposal {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*AdminMultisigProposal
	for _, p := range s.Proposals {
		if p.Status == AdminMultisigPending {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

package core

import (
	"errors"
	"sync"
)

// VoteRecord is one address's cast vote on one proposal - the
// permanent, public "community remembers" record (see
// api/governance_social.go's HandleGovernanceVotesForUser). Kept
// alongside, not instead of, the existing Voted bool map - Voted stays
// the fast "has this address already voted" check every apply-time
// guard already uses; VotesByAddress is purely additive, for history
// display.
type VoteRecord struct {
	ProposalID    string
	ProposalTitle string
	Support       bool
	Weight        uint64
	Timestamp     int64
}

// ProposalStore is the canonical, shared registry of governance
// Proposals and who has already voted on each one. It is the single
// owner of vote-tallying so every node reads the same result.
type ProposalStore struct {
	mu             sync.Mutex
	Proposals      map[string]*Proposal
	Voted          map[string]map[string]bool // proposalID -> voter address -> voted
	VotesByAddress map[string][]VoteRecord    // voter address -> their full vote history, oldest first
}

func NewProposalStore() *ProposalStore {
	return &ProposalStore{
		Proposals:      make(map[string]*Proposal),
		Voted:          make(map[string]map[string]bool),
		VotesByAddress: make(map[string][]VoteRecord),
	}
}

func (ps *ProposalStore) Create(p *Proposal) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.Proposals[p.ID] = p
}

func (ps *ProposalStore) Count() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	return len(ps.Proposals)
}

// Get returns a single proposal, for fast-fail validation before a
// vote transaction is submitted (the real, enforced check happens in
// Vote at confirmation time).
func (ps *ProposalStore) Get(proposalID string) (*Proposal, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, ok := ps.Proposals[proposalID]
	return p, ok
}

// HasVoted reports whether address has already voted on proposalID.
func (ps *ProposalStore) HasVoted(proposalID, address string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	return ps.Voted[proposalID][address]
}

// VotesForAddress returns one address's full vote history, oldest
// first - the thread-safe read path for VotesByAddress (see
// api/governance_social.go's HandleGovernanceVotesForUser), rather
// than reading the map field directly from outside the package.
func (ps *ProposalStore) VotesForAddress(address string) []VoteRecord {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	return ps.VotesByAddress[address]
}

func (ps *ProposalStore) List() []*Proposal {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	list := make([]*Proposal, 0, len(ps.Proposals))
	for _, p := range ps.Proposals {
		list = append(list, p)
	}
	return list
}

// Vote applies a single address's vote to a proposal exactly once,
// using their current Wallet.VotingPower as weight. timestamp is the
// authoring transaction's own timestamp (never wall-clock), matching
// this codebase's usual cross-node determinism discipline - it's only
// used for VoteRecord display, never for any consensus-affecting
// decision.
func (ps *ProposalStore) Vote(proposalID, address string, support bool, power uint64, timestamp int64) (*Proposal, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, ok := ps.Proposals[proposalID]
	if !ok {
		return nil, errors.New("proposal not found")
	}

	if ps.Voted[proposalID][address] {
		return nil, errors.New("address already voted on this proposal")
	}

	if support {
		p.VoteYes(power)
	} else {
		p.VoteNo(power)
	}

	if ps.Voted[proposalID] == nil {
		ps.Voted[proposalID] = make(map[string]bool)
	}
	ps.Voted[proposalID][address] = true

	ps.VotesByAddress[address] = append(ps.VotesByAddress[address], VoteRecord{
		ProposalID:    proposalID,
		ProposalTitle: p.Title,
		Support:       support,
		Weight:        power,
		Timestamp:     timestamp,
	})

	return p, nil
}

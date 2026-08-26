package core

import "sync"

// ExecutiveProposal is a narrower voting lane than the ordinary
// Proposal (core/governance.go): only current top-N-reputation
// "executive" wallets (see api/rc_executive.go's isExecutive) may vote,
// no broad community quorum, passes by simple headcount majority of
// executives who actually voted - a deliberate judgment call, since
// this is already a small, reputation-pre-selected committee, not the
// general electorate. Built as a ParamKey/ParamValue pair rather than a
// single-purpose struct so a future second executive-only parameter
// doesn't need a new type, but ParamKey is fixed server-side (see
// api/executive_proposal.go's realWorldTrustWeightParamKey) to
// "RealWorldTrustWeightBps" for this phase - not client-open-ended.
type ExecutiveProposal struct {
	ID        string
	CreatedBy string

	ParamKey   string
	ParamValue uint64
	// TargetAddress is only meaningful when ParamKey is the
	// moderator-deactivation action (see api/executive_proposal.go's
	// moderatorDeactivateParamKey) - empty for every other ParamKey.
	TargetAddress string

	Open     bool
	Deadline int64
	Status   ProposalStatus

	// YesVotes/NoVotes are a plain headcount (one executive, one vote)
	// - NOT weighted by votingWeight(), unlike the ordinary Proposal
	// lane, since the committee itself is already the selection
	// mechanism.
	YesVotes uint64
	NoVotes  uint64
	Voted    map[string]bool

	CreatedAt int64
}

type ExecutiveProposalStore struct {
	mu        sync.Mutex
	Proposals map[string]*ExecutiveProposal
}

func NewExecutiveProposalStore() *ExecutiveProposalStore {
	return &ExecutiveProposalStore{Proposals: make(map[string]*ExecutiveProposal)}
}

func (s *ExecutiveProposalStore) Create(p *ExecutiveProposal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Voted == nil {
		p.Voted = make(map[string]bool)
	}
	s.Proposals[p.ID] = p
}

func (s *ExecutiveProposalStore) Get(id string) (*ExecutiveProposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Proposals[id]
	return p, ok
}

func (s *ExecutiveProposalStore) List() []*ExecutiveProposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ExecutiveProposal, 0, len(s.Proposals))
	for _, p := range s.Proposals {
		out = append(out, p)
	}
	return out
}

// Vote records address's headcount vote - one vote per address per
// proposal (mirrors ProposalStore.Voted's dedup convention), only while
// still Open. Returns false (no-op) on a double-vote attempt or a
// closed/missing proposal - the caller (applyExecutiveProposalVote)
// re-checks isExecutive itself before ever calling this.
func (s *ExecutiveProposalStore) Vote(id, address string, support bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Proposals[id]
	if !ok || !p.Open {
		return false
	}
	if p.Voted[address] {
		return false
	}
	p.Voted[address] = true
	if support {
		p.YesVotes++
	} else {
		p.NoVotes++
	}
	return true
}

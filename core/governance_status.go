package core

import "sync"

// GovernanceStatus is a free-text update posted by a governance-
// activated wallet (VotingPower > 0) - purely informational, like
// NegotiationMessage/HubNotification, so it's node-local API state
// broadcast to the public landing page's live news corner rather than
// part of the on-chain proposal/vote record.
type GovernanceStatus struct {
	ID        string
	Address   string
	Text      string
	Timestamp int64
}

type GovernanceStatusStore struct {
	mu    sync.Mutex
	byID  map[string]*GovernanceStatus
	order []string // insertion order, oldest first
}

func NewGovernanceStatusStore() *GovernanceStatusStore {
	return &GovernanceStatusStore{byID: make(map[string]*GovernanceStatus)}
}

func (s *GovernanceStatusStore) Add(gs *GovernanceStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID[gs.ID] = gs
	s.order = append(s.order, gs.ID)
}

// Recent returns up to n most-recently-posted statuses, newest first -
// what both the Governance page and the public landing page's live
// popup poll.
func (s *GovernanceStatusStore) Recent(n int) []*GovernanceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*GovernanceStatus, 0, n)
	for i := len(s.order) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, s.byID[s.order[i]])
	}
	return out
}

// Snapshot/Restore support persistence.go saving/loading every status
// across a restart, in original post order.
func (s *GovernanceStatusStore) Snapshot() []*GovernanceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*GovernanceStatus, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id])
	}
	return out
}

func (s *GovernanceStatusStore) Restore(all []*GovernanceStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, gs := range all {
		s.byID[gs.ID] = gs
		s.order = append(s.order, gs.ID)
	}
}

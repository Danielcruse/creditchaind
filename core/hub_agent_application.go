package core

import "sync"

type AgentApplicationStatus string

const (
	AgentApplicationPending  AgentApplicationStatus = "PENDING"
	AgentApplicationApproved AgentApplicationStatus = "APPROVED"
	AgentApplicationRejected AgentApplicationStatus = "REJECTED"
)

// HubAgentApplication is an outernet user applying to become one of a
// Trading Hub's workers/agents - the missing self-serve half of
// core.HubWorker, which today can only ever be created by the hub
// itself inviting someone (see HubWorker's doc comment). Applying
// costs nothing and moves no value, so like HubNotification/
// NegotiationMessage this is node-local API state, not a
// block-confirmed transaction - the real access grant only happens
// when the hub owner approves, which goes through the exact same
// TxHubWorkerAdd path a manual invite already uses (see
// api/hub_agents.go's HandleApproveHubAgent).
type HubAgentApplication struct {
	ID                 string
	HubAddress         string
	ApplicantAddress   string
	ApplicantPublicKey string
	Message            string
	Status             AgentApplicationStatus
	CreatedAt          int64
	DecidedAt          int64
}

type HubAgentApplicationStore struct {
	mu   sync.Mutex
	Apps map[string]*HubAgentApplication
}

func NewHubAgentApplicationStore() *HubAgentApplicationStore {
	return &HubAgentApplicationStore{Apps: make(map[string]*HubAgentApplication)}
}

func (s *HubAgentApplicationStore) Add(a *HubAgentApplication) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Apps[a.ID] = a
}

func (s *HubAgentApplicationStore) Get(id string) (*HubAgentApplication, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[id]
	return a, ok
}

// HasPendingApplication stops the same wallet from spamming a hub with
// duplicate applications while one is still awaiting a decision.
func (s *HubAgentApplicationStore) HasPendingApplication(hub, applicant string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range s.Apps {
		if a.HubAddress == hub && a.ApplicantAddress == applicant && a.Status == AgentApplicationPending {
			return true
		}
	}
	return false
}

// ListForHub returns every application ever submitted to a hub,
// any status, newest first - same "nothing disappears" convention as
// every other List in this codebase.
func (s *HubAgentApplicationStore) ListForHub(hub string) []*HubAgentApplication {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*HubAgentApplication
	for _, a := range s.Apps {
		if a.HubAddress == hub {
			out = append(out, a)
		}
	}
	return out
}

// Decide flips a PENDING application to APPROVED/REJECTED exactly
// once - re-deciding an already-decided application is rejected, same
// "exactly once" convention as NegotiationStore.Cancel and friends.
func (s *HubAgentApplicationStore) Decide(id string, approve bool, decidedAt int64) (*HubAgentApplication, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[id]
	if !ok || a.Status != AgentApplicationPending {
		return nil, false
	}

	if approve {
		a.Status = AgentApplicationApproved
	} else {
		a.Status = AgentApplicationRejected
	}
	a.DecidedAt = decidedAt

	return a, true
}

func (s *HubAgentApplicationStore) Snapshot() []*HubAgentApplication {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*HubAgentApplication, 0, len(s.Apps))
	for _, a := range s.Apps {
		out = append(out, a)
	}
	return out
}

func (s *HubAgentApplicationStore) Restore(all []*HubAgentApplication) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range all {
		s.Apps[a.ID] = a
	}
}

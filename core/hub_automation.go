package core

import "sync"

// HubAutomation tracks one hub's paid "Automation" tier subscription -
// see api/hub_automation.go's doc comment for the full scoped-
// automation design (canned-reply chat responses + capped auto-
// confirm-paid, never a general scripting/smart-contract surface).
// A lapsed subscription (ActiveUntil in the past) silently reverts
// every one of its workers' automation capabilities to inactive - no
// separate deactivation step needed, since every check re-verifies
// IsActive at the moment of use.
type HubAutomation struct {
	Hub         string
	ActiveUntil int64
}

type HubAutomationStore struct {
	mu   sync.Mutex
	Subs map[string]*HubAutomation // keyed by hub address
}

func NewHubAutomationStore() *HubAutomationStore {
	return &HubAutomationStore{Subs: make(map[string]*HubAutomation)}
}

// Subscribe sets/extends hub's subscription to now + durationSeconds
// (not cumulative - always a fresh 30-day-from-now window, matching
// this codebase's existing "always the whole record" convention).
func (s *HubAutomationStore) Subscribe(hub string, activeUntil int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Subs[hub] = &HubAutomation{Hub: hub, ActiveUntil: activeUntil}
}

// IsActive reports whether hub's Automation-tier subscription is
// currently valid as of asOf (the block timestamp, never wall-clock,
// per this codebase's determinism convention).
func (s *HubAutomationStore) IsActive(hub string, asOf int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.Subs[hub]
	return ok && asOf < sub.ActiveUntil
}

func (s *HubAutomationStore) Get(hub string) (*HubAutomation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.Subs[hub]
	return sub, ok
}

func (s *HubAutomationStore) Snapshot() map[string]*HubAutomation {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*HubAutomation, len(s.Subs))
	for k, v := range s.Subs {
		out[k] = v
	}
	return out
}

func (s *HubAutomationStore) Restore(all map[string]*HubAutomation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Subs[k] = v
	}
}

package core

import "sync"

// GovernanceParamsStore holds protocol parameters that a passed
// PARAM_CHANGE governance proposal can override - see
// api/apply.go's processProposalFinalization, the only writer.
// Deliberately starts with a small, explicitly-named set (see
// api/governance_params.go's seeding) rather than a full migration of
// every tunable constant in the codebase - PoSR's own consensus/
// slashing constants are named as deliberate future work, not
// touched here, to avoid repeating the same night's scope-creep
// pattern that caused a real production incident when PoSR shipped.
type GovernanceParamsStore struct {
	mu     sync.Mutex
	params map[string]uint64
}

func NewGovernanceParamsStore() *GovernanceParamsStore {
	return &GovernanceParamsStore{params: make(map[string]uint64)}
}

// Seed sets a parameter's starting value ONLY if it hasn't already
// been set - safe to call at every startup without clobbering a
// value a passed proposal already changed and that got restored from
// persistence.
func (s *GovernanceParamsStore) Seed(key string, value uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.params[key]; !ok {
		s.params[key] = value
	}
}

// Get returns a parameter's current value and whether it's been set
// at all (seeded or overridden) - callers fall back to their own
// hardcoded default when ok is false, so a never-seeded key never
// silently reads as zero.
func (s *GovernanceParamsStore) Get(key string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.params[key]
	return v, ok
}

// Set overrides a parameter's value - called only from
// processProposalFinalization's PASSED branch for a PARAM_CHANGE
// proposal, so a governance-set value only ever takes effect after a
// real, quorum-meeting, majority-yes vote.
func (s *GovernanceParamsStore) Set(key string, value uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params[key] = value
}

// Snapshot returns a copy of every currently-set parameter, for
// persistence and for a read-only "current governance parameters"
// API response.
func (s *GovernanceParamsStore) Snapshot() map[string]uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]uint64, len(s.params))
	for k, v := range s.params {
		out[k] = v
	}
	return out
}

// Restore replaces every parameter from a persisted snapshot.
func (s *GovernanceParamsStore) Restore(snapshot map[string]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params = make(map[string]uint64, len(snapshot))
	for k, v := range snapshot {
		s.params[k] = v
	}
}

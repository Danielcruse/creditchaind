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
	// setAt tracks, per key, the transaction timestamp of the most
	// recent SetAt call - see SetAt's doc comment. Deliberately a
	// SEPARATE map from params (not folded into a single struct value)
	// so every existing plain Set/Get call site (the vast majority of
	// this store's callers - most governance params have no staleness
	// requirement) is completely unaffected by this addition.
	setAt map[string]int64
}

func NewGovernanceParamsStore() *GovernanceParamsStore {
	return &GovernanceParamsStore{params: make(map[string]uint64), setAt: make(map[string]int64)}
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

// GetOrDefault is a convenience wrapper around Get for read sites that
// don't need to distinguish "never set" from "set to this value" -
// they just want today's hardcoded constant until an admin or a
// passed PARAM_CHANGE proposal overrides it.
func (s *GovernanceParamsStore) GetOrDefault(key string, def uint64) uint64 {
	if v, ok := s.Get(key); ok {
		return v
	}
	return def
}

// Set overrides a parameter's value - called from
// processProposalFinalization's PASSED branch for a PARAM_CHANGE
// proposal (so a governance-set value only ever takes effect after a
// real, quorum-meeting, majority-yes vote) and from a handful of admin/
// executive fast paths. Clears any previously-recorded SetAt timestamp
// for this key, so a value written through this plain path never reads
// as falsely "freshly confirmed" by IsStale below - callers that care
// about staleness (currently just EthUsdCentsPerEth, see
// api/apply.go's collateralValueInYen) must go through SetAt instead.
func (s *GovernanceParamsStore) Set(key string, value uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params[key] = value
	delete(s.setAt, key)
}

// SetAt is Set plus a recorded timestamp - the real write path for any
// key IsStale is ever checked against. `at` must be a deterministic
// transaction timestamp (tx.Timestamp in an Apply* function), never
// time.Now(), so every node persists the identical value - the same
// discipline every other consensus-critical timestamp in this codebase
// follows.
func (s *GovernanceParamsStore) SetAt(key string, value uint64, at int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params[key] = value
	s.setAt[key] = at
}

// LastSetAt returns when a key was last written via SetAt (never Set) -
// ok is false if it's never been SetAt at all (never set through the
// timestamped path, or only ever set via the plain Set path above).
func (s *GovernanceParamsStore) LastSetAt(key string) (at int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok = s.setAt[key]
	return at, ok
}

// IsStale reports whether key's value is either never SetAt, or was
// last SetAt more than maxAgeSeconds before asOf - the shared "is this
// price too old to trust" check for any staleness-sensitive governance
// parameter. asOf must be the same deterministic timestamp basis every
// caller already uses (a block/transaction timestamp in consensus-
// critical code, or time.Now().Unix() in a pure read-only HTTP
// pre-check) - never mixed within a single evaluation.
func (s *GovernanceParamsStore) IsStale(key string, asOf int64, maxAgeSeconds int64) bool {
	at, ok := s.LastSetAt(key)
	if !ok {
		return true
	}
	return asOf-at > maxAgeSeconds
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

// SnapshotSetAt is Snapshot's counterpart for the setAt timestamps -
// persisted as a separate top-level field (see api/persistence.go's
// ChainSnapshot.GovernanceParamsSetAt) so loading an OLDER, pre-this-
// feature state.json (which has no timestamps at all) correctly leaves
// every key with no recorded SetAt - IsStale then reports true until an
// admin/proposal next actually SetAt's it, the safe default rather than
// assuming a value nobody has re-confirmed since this upgrade is fresh.
func (s *GovernanceParamsStore) SnapshotSetAt() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.setAt))
	for k, v := range s.setAt {
		out[k] = v
	}
	return out
}

// Restore replaces every parameter from a persisted snapshot - clears
// setAt entirely (matching Set's own clear-on-plain-write behavior)
// since Restore's caller (api/persistence.go's state.json load) always
// calls RestoreSetAt right after with whatever timestamps actually were
// persisted, if any.
func (s *GovernanceParamsStore) Restore(snapshot map[string]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params = make(map[string]uint64, len(snapshot))
	for k, v := range snapshot {
		s.params[k] = v
	}
	s.setAt = make(map[string]int64)
}

// RestoreSetAt restores the setAt timestamps from a persisted
// snapshot - a no-op-safe empty/nil map for state.json files written
// before this feature existed.
func (s *GovernanceParamsStore) RestoreSetAt(snapshot map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setAt = make(map[string]int64, len(snapshot))
	for k, v := range snapshot {
		s.setAt[k] = v
	}
}

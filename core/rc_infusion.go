package core

import "sync"

// RCInfusion is a small, append-only, publicly-listable record of a
// wallet voluntarily burning its own RC (REPT) and minting the same
// amount into the governance RC pool (api/rc_governance_pool_mining.go)
// as a self-declared statement of "real world trust" - TrustReason is
// explicitly self-attested, never independently verified, mirroring
// the existing PegReference metadata-only precedent from the Token
// Standards work (core/token_standard.go).
type RCInfusion struct {
	Address     string
	RCAmount    uint64
	TrustReason string
	CreatedAt   int64
}

type RCInfusionStore struct {
	mu        sync.Mutex
	Infusions []*RCInfusion
}

func NewRCInfusionStore() *RCInfusionStore {
	return &RCInfusionStore{}
}

func (s *RCInfusionStore) Record(inf *RCInfusion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Infusions = append(s.Infusions, inf)
}

// Recent returns up to the last n infusions, oldest of the returned
// slice first - mirrors GovernanceStatusStore's own Recent(n) shape.
func (s *RCInfusionStore) Recent(n int) []*RCInfusion {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Infusions) <= n {
		out := make([]*RCInfusion, len(s.Infusions))
		copy(out, s.Infusions)
		return out
	}
	out := make([]*RCInfusion, n)
	copy(out, s.Infusions[len(s.Infusions)-n:])
	return out
}

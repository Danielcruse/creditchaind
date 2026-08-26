package core

import "sync"

// PredictionSide is which way a wallet backed a PREDICTION-kind
// Proposal with RC - separate from the proposal's own ordinary
// Yes/No headcount vote (see core.Proposal.VoteYes/VoteNo), which
// every voter still casts regardless of whether they also attach RC.
type PredictionSide string

const (
	PredictionSideYes PredictionSide = "YES"
	PredictionSideNo  PredictionSide = "NO"
)

// PredictionInfusion is one wallet's RC stake on one side of one
// prediction - mirrors RCInfusion's shape (core/rc_infusion.go),
// append-only, publicly listable.
type PredictionInfusion struct {
	ProposalID string
	Address    string
	Side       PredictionSide
	RCAmount   uint64
	CreatedAt  int64
}

// PredictionPool is the live, visible RC bet pool for one PREDICTION
// proposal - created lazily on the first RC-backed vote (see
// api/prediction_vote.go's applyPredictionVote), never before. A
// PREDICTION proposal with zero RC votes never gets one of these at
// all, and finalizes as an ordinary plain vote.
type PredictionPool struct {
	ProposalID  string
	YesPoolRC   uint64
	NoPoolRC    uint64
	Infusions   []*PredictionInfusion
	Resolved    bool
	WinningSide PredictionSide // only meaningful once Resolved
	BonusMinted uint64         // the 14-day patience bonus actually minted, 0 if never triggered
}

type PredictionPoolStore struct {
	mu    sync.Mutex
	Pools map[string]*PredictionPool
}

func NewPredictionPoolStore() *PredictionPoolStore {
	return &PredictionPoolStore{Pools: make(map[string]*PredictionPool)}
}

// Get returns the pool for a proposal, or nil if it doesn't exist yet
// (no RC has been infused on this prediction).
func (s *PredictionPoolStore) Get(proposalID string) *PredictionPool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Pools[proposalID]
}

// Infuse records one wallet's RC stake, lazily creating the pool on
// its first call for a given proposal - the moment "a pool instantly
// forms" in the product sense.
func (s *PredictionPoolStore) Infuse(inf *PredictionInfusion) *PredictionPool {
	s.mu.Lock()
	defer s.mu.Unlock()

	pool, ok := s.Pools[inf.ProposalID]
	if !ok {
		pool = &PredictionPool{ProposalID: inf.ProposalID}
		s.Pools[inf.ProposalID] = pool
	}

	if inf.Side == PredictionSideYes {
		pool.YesPoolRC += inf.RCAmount
	} else {
		pool.NoPoolRC += inf.RCAmount
	}
	pool.Infusions = append(pool.Infusions, inf)
	return pool
}

// Snapshot/Restore support persistence.go saving/loading every pool
// across a restart, same pattern every other store in this codebase
// already follows.
func (s *PredictionPoolStore) Snapshot() map[string]*PredictionPool {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*PredictionPool, len(s.Pools))
	for id, p := range s.Pools {
		out[id] = p
	}
	return out
}

func (s *PredictionPoolStore) Restore(all map[string]*PredictionPool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, p := range all {
		s.Pools[id] = p
	}
}

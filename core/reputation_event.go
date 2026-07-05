package core

import "sync"

// ReputationEvent records the reputation change one specific,
// already-confirmed transaction caused - a side-channel log, not a
// field on Transaction itself, since block.Transactions is a plain
// value slice applied by-value all the way down through
// ApplyTransaction (see api/apply.go's ApplyBlock/applyTxOnce) with no
// pointer back to the stored copy. Recording this separately, keyed by
// the transaction's own ID, lets the explorer attach "this transaction
// earned +14 reputation" without requiring an invasive refactor of
// every apply* function's signature.
//
// Target is deliberately its own field rather than assuming "always
// From" or "always To" - some reputation-bearing transactions affect
// a THIRD address entirely (e.g. a LOAN_OFFER_DRAW's From is the
// borrower drawing capital, but the +2 reputation for funding it
// lands on the offer's Lender, who never signed this transaction at
// all).
type ReputationEvent struct {
	TxID      string
	Target    string
	Delta     int64 // positive = gain, negative = loss
	Reason    string
	Timestamp int64
}

type ReputationEventStore struct {
	mu     sync.Mutex
	byTxID map[string]*ReputationEvent
}

func NewReputationEventStore() *ReputationEventStore {
	return &ReputationEventStore{byTxID: make(map[string]*ReputationEvent)}
}

func (s *ReputationEventStore) Record(e *ReputationEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byTxID[e.TxID] = e
}

func (s *ReputationEventStore) Get(txID string) (*ReputationEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.byTxID[txID]
	return e, ok
}

func (s *ReputationEventStore) Snapshot() map[string]*ReputationEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*ReputationEvent, len(s.byTxID))
	for k, v := range s.byTxID {
		out[k] = v
	}
	return out
}

func (s *ReputationEventStore) Restore(all map[string]*ReputationEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.byTxID[k] = v
	}
}

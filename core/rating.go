package core

import "sync"

// Rating is one wallet's site rating - one per address, resubmitting
// overwrites rather than double-submits (matches WalletStore.Register's
// existing idempotent-registration convention). Stars 1-5; a rating of
// 4 or 5 is what api/apply.go's applyRatingSubmit treats as "good"
// enough to pay out - see that function's doc comment for why 4 was
// chosen as the cutoff, not just 5 (a judgment call, not derived from
// a specified model).
type Rating struct {
	Address   string
	Stars     uint8
	Comment   string
	CreatedAt int64
}

type RatingStore struct {
	mu      sync.Mutex
	Ratings map[string]*Rating // address -> their one rating
	order   []string           // insertion order of first-ever rating per address, for Recent()
}

func NewRatingStore() *RatingStore {
	return &RatingStore{Ratings: make(map[string]*Rating)}
}

// Set records or overwrites address's rating. Returns true if this is
// the address's first-ever rating (the only case api/apply.go's
// applyRatingSubmit ever pays CCT for).
func (s *RatingStore) Set(r *Rating) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, existed := s.Ratings[r.Address]
	s.Ratings[r.Address] = r
	if !existed {
		s.order = append(s.order, r.Address)
	}
	return !existed
}

func (s *RatingStore) Get(address string) (*Rating, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Ratings[address]
	return r, ok
}

// Recent returns up to n most-recently-first-rated wallets, newest
// first - same shape as GovernanceStatusStore.Recent/AnnouncementStore.Recent.
func (s *RatingStore) Recent(n int) []*Rating {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Rating, 0, n)
	for i := len(s.order) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, s.Ratings[s.order[i]])
	}
	return out
}

func (s *RatingStore) Snapshot() []*Rating {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Rating, 0, len(s.order))
	for _, addr := range s.order {
		out = append(out, s.Ratings[addr])
	}
	return out
}

func (s *RatingStore) Restore(all []*Rating) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range all {
		if _, existed := s.Ratings[r.Address]; !existed {
			s.order = append(s.order, r.Address)
		}
		s.Ratings[r.Address] = r
	}
}

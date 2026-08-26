package core

import "sync"

// TradeReview is one counterparty's review of the other, submitted
// after a specific P2P offer settles successfully (see
// core/p2p_offer.go's SettlementStatus - CONFIRMED, AUTO_RELEASED, and
// RESOLVED_RELEASE all count). Distinct from Rating (core/rating.go),
// which is a one-per-wallet, one-time site rating unrelated to any
// specific trade or counterparty - this is per-trade, per-reviewer,
// and feeds directly into the reviewee's own reputation (bounded, see
// TradeReviewStore.MarkRewarded's doc comment).
type TradeReview struct {
	TradeID   string
	Reviewer  string
	Reviewee  string
	Stars     uint8 // 1-5
	Comment   string
	CreatedAt int64
}

// TradeReviewStore is keyed by TradeID+Reviewer (one review per
// reviewer per trade - a trade has at most two reviews total, one per
// side) - Reviews[tradeID][reviewer]. RewardedPairs separately tracks
// which DIRECTED reviewer->reviewee pairs have ever paid out a
// reputation bonus, capping it at once per pair, ever, regardless of
// how many more times those same two wallets go on to trade with each
// other - the deliberate anti-farming measure closing the obvious
// "two colluding wallets repeatedly trade and rate each other 5 stars"
// exploit an uncapped per-trade bonus would otherwise open, given
// reputation is 55% of PoSR validator-selection weight
// (core/consensus.go) and directly feeds governance voting power.
type TradeReviewStore struct {
	mu            sync.Mutex
	Reviews       map[string]map[string]*TradeReview // tradeID -> reviewer -> review
	RewardedPairs map[string]bool                    // "reviewer|reviewee" -> ever paid a reputation bonus
}

func NewTradeReviewStore() *TradeReviewStore {
	return &TradeReviewStore{
		Reviews:       make(map[string]map[string]*TradeReview),
		RewardedPairs: make(map[string]bool),
	}
}

func pairKey(reviewer, reviewee string) string {
	return reviewer + "|" + reviewee
}

// HasReviewed reports whether reviewer has already submitted a review
// for tradeID - the one-review-per-side-per-trade gate.
func (s *TradeReviewStore) HasReviewed(tradeID, reviewer string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.Reviews[tradeID][reviewer]
	return ok
}

// Submit records review - caller (applyTradeReview) has already
// validated the trade, the reviewer's participation in it, and that
// this reviewer hasn't already reviewed this trade. Returns false
// (no-op) if a review from this reviewer for this trade already
// exists, matching this codebase's idempotent-write convention.
func (s *TradeReviewStore) Submit(review *TradeReview) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Reviews[review.TradeID] == nil {
		s.Reviews[review.TradeID] = make(map[string]*TradeReview)
	}
	if _, exists := s.Reviews[review.TradeID][review.Reviewer]; exists {
		return false
	}
	s.Reviews[review.TradeID][review.Reviewer] = review
	return true
}

// PairAlreadyRewarded reports whether reviewer has ever before earned
// reviewee a reputation bonus - the anti-farming cap.
func (s *TradeReviewStore) PairAlreadyRewarded(reviewer, reviewee string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.RewardedPairs[pairKey(reviewer, reviewee)]
}

// MarkRewarded records that reviewer->reviewee has now paid out its
// one-time reputation bonus - called immediately before actually
// crediting the reputation (applyTradeReview), never speculatively.
func (s *TradeReviewStore) MarkRewarded(reviewer, reviewee string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.RewardedPairs[pairKey(reviewer, reviewee)] = true
}

// ForReviewee returns every review reviewee has ever received, across
// every trade - what a profile page shows.
func (s *TradeReviewStore) ForReviewee(reviewee string) []*TradeReview {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*TradeReview
	for _, byReviewer := range s.Reviews {
		for _, r := range byReviewer {
			if r.Reviewee == reviewee {
				out = append(out, r)
			}
		}
	}
	return out
}

// ForTrade returns every review submitted for tradeID (0, 1, or 2 of
// them) - what the trade chat itself checks to decide whether to show
// "you already reviewed" instead of the review prompt.
func (s *TradeReviewStore) ForTrade(tradeID string) []*TradeReview {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*TradeReview
	for _, r := range s.Reviews[tradeID] {
		out = append(out, r)
	}
	return out
}

// Snapshot/Restore persist review + reward-pair state across restarts,
// same pattern as every other store's persistence helpers.
type TradeReviewSnapshot struct {
	Reviews       []*TradeReview
	RewardedPairs []string
}

func (s *TradeReviewStore) Snapshot() TradeReviewSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	var reviews []*TradeReview
	for _, byReviewer := range s.Reviews {
		for _, r := range byReviewer {
			reviews = append(reviews, r)
		}
	}
	var pairs []string
	for k := range s.RewardedPairs {
		pairs = append(pairs, k)
	}
	return TradeReviewSnapshot{Reviews: reviews, RewardedPairs: pairs}
}

func (s *TradeReviewStore) Restore(snap TradeReviewSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range snap.Reviews {
		if s.Reviews[r.TradeID] == nil {
			s.Reviews[r.TradeID] = make(map[string]*TradeReview)
		}
		s.Reviews[r.TradeID][r.Reviewer] = r
	}
	for _, k := range snap.RewardedPairs {
		s.RewardedPairs[k] = true
	}
}

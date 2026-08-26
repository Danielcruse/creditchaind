package core

import "testing"

func TestTradeReviewStore_SubmitIsIdempotentPerReviewerPerTrade(t *testing.T) {
	s := NewTradeReviewStore()

	first := s.Submit(&TradeReview{TradeID: "t1", Reviewer: "a", Reviewee: "b", Stars: 5})
	if !first {
		t.Fatal("expected the first review from a for trade t1 to succeed")
	}
	second := s.Submit(&TradeReview{TradeID: "t1", Reviewer: "a", Reviewee: "b", Stars: 1})
	if second {
		t.Fatal("expected a second review from the same reviewer for the same trade to be rejected")
	}
	if !s.HasReviewed("t1", "a") {
		t.Fatal("expected HasReviewed to report true after a successful Submit")
	}

	// A different reviewer (the other side of the same trade) can still
	// submit their own review.
	if !s.Submit(&TradeReview{TradeID: "t1", Reviewer: "b", Reviewee: "a", Stars: 4}) {
		t.Fatal("expected the counterparty's own review of the same trade to succeed")
	}
	if len(s.ForTrade("t1")) != 2 {
		t.Fatalf("expected 2 reviews recorded for t1, got %d", len(s.ForTrade("t1")))
	}
}

func TestTradeReviewStore_PairRewardCappedOncePerDirectedPair(t *testing.T) {
	s := NewTradeReviewStore()

	if s.PairAlreadyRewarded("a", "b") {
		t.Fatal("expected a fresh pair to not be marked rewarded")
	}
	s.MarkRewarded("a", "b")
	if !s.PairAlreadyRewarded("a", "b") {
		t.Fatal("expected the pair to be marked rewarded after MarkRewarded")
	}
	// The reverse direction is a DIFFERENT pair key - b reviewing a for
	// the first time still earns its own bonus.
	if s.PairAlreadyRewarded("b", "a") {
		t.Fatal("expected the reverse direction to be independently un-rewarded")
	}
}

func TestTradeReviewStore_ForRevieweeAggregatesAcrossTrades(t *testing.T) {
	s := NewTradeReviewStore()
	s.Submit(&TradeReview{TradeID: "t1", Reviewer: "a", Reviewee: "target", Stars: 5})
	s.Submit(&TradeReview{TradeID: "t2", Reviewer: "c", Reviewee: "target", Stars: 3})
	s.Submit(&TradeReview{TradeID: "t3", Reviewer: "d", Reviewee: "someone-else", Stars: 5})

	got := s.ForReviewee("target")
	if len(got) != 2 {
		t.Fatalf("expected 2 reviews for target, got %d", len(got))
	}
}

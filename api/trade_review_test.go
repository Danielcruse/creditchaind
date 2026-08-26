package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func resetTradeReviewTestState() {
	resetApplyTestState()
	p2pOfferStore = core.NewP2POfferStore()
	tradeReviewStore = core.NewTradeReviewStore()
}

func buildSettledP2POffer(id, seller, buyer string, status core.SettlementStatus) *core.P2POffer {
	offer := &core.P2POffer{
		ID:               id,
		Creator:          seller,
		OfferSymbol:      "CCT",
		OfferAmount:      20,
		WantSymbol:       "NGN",
		WantAmount:       59384640,
		PaymentMode:      core.PaymentModeExternal,
		Status:           core.OfferAccepted,
		AcceptedBy:       buyer,
		SettlementStatus: status,
	}
	p2pOfferStore.Offers[id] = offer
	return offer
}

func tradeReviewTx(id, from, tradeID string, stars uint8) core.Transaction {
	data, _ := json.Marshal(tradeReviewData{TradeID: tradeID, Stars: stars, Comment: ""})
	return core.Transaction{ID: id, From: from, Type: core.TxTradeReview, Data: string(data), Timestamp: 1000}
}

// TestApplyTradeReview_PositiveReviewGrantsReputationOnce is the core
// end-to-end regression test: a first-time positive review grants the
// reviewee reputation, but a second trade between the same pair does
// not pay out again - the anti-farming cap the user explicitly asked
// for.
func TestApplyTradeReview_PositiveReviewGrantsReputationOnce(t *testing.T) {
	resetTradeReviewTestState()

	const seller, buyer = "seller-addr", "buyer-addr"
	buildSettledP2POffer("trade-1", seller, buyer, core.SettlementConfirmed)
	sellerWallet := walletStore.Get(seller)
	startingReputation := sellerWallet.Reputation

	applyTradeReview(tradeReviewTx("tx1", buyer, "trade-1", 5))

	if sellerWallet.Reputation != startingReputation+TradeReviewReputationBonus {
		t.Fatalf("expected seller reputation to grow by %d, got %d -> %d", TradeReviewReputationBonus, startingReputation, sellerWallet.Reputation)
	}
	if !tradeReviewStore.PairAlreadyRewarded(buyer, seller) {
		t.Fatal("expected the buyer->seller pair to be marked rewarded")
	}

	// A second, separate trade between the SAME two wallets - the
	// review is still recorded, but no additional reputation.
	buildSettledP2POffer("trade-2", seller, buyer, core.SettlementConfirmed)
	afterFirstReward := sellerWallet.Reputation
	applyTradeReview(tradeReviewTx("tx2", buyer, "trade-2", 5))

	if sellerWallet.Reputation != afterFirstReward {
		t.Fatalf("expected no additional reputation from a second trade with the same reviewer, got %d -> %d", afterFirstReward, sellerWallet.Reputation)
	}
	if len(tradeReviewStore.ForReviewee(seller)) != 2 {
		t.Fatalf("expected both reviews to still be recorded (2), got %d", len(tradeReviewStore.ForReviewee(seller)))
	}
}

// TestApplyTradeReview_LowStarsRecordedButNoReputation confirms a 1-3
// star review is stored (visible track record) but grants nothing -
// only genuinely positive feedback pays out, same reasoning as
// applyModeratorConfirm only rewarding an undisputed confirmation.
func TestApplyTradeReview_LowStarsRecordedButNoReputation(t *testing.T) {
	resetTradeReviewTestState()

	const seller, buyer = "seller-addr", "buyer-addr"
	buildSettledP2POffer("trade-1", seller, buyer, core.SettlementAutoReleased)
	startingReputation := walletStore.Get(seller).Reputation

	applyTradeReview(tradeReviewTx("tx1", buyer, "trade-1", 2))

	if got := walletStore.Get(seller).Reputation; got != startingReputation {
		t.Fatalf("expected no reputation change from a 2-star review, got %d -> %d", startingReputation, got)
	}
	if len(tradeReviewStore.ForReviewee(seller)) != 1 {
		t.Fatal("expected the low-star review to still be recorded")
	}
}

// TestApplyTradeReview_RejectsNonParticipantAndUnsettledTrade covers
// the two structural gates: only an actual party to the trade can
// review it, and only once it's actually settled successfully.
func TestApplyTradeReview_RejectsNonParticipantAndUnsettledTrade(t *testing.T) {
	resetTradeReviewTestState()

	const seller, buyer, outsider = "seller-addr", "buyer-addr", "outsider-addr"
	buildSettledP2POffer("trade-1", seller, buyer, core.SettlementConfirmed)

	applyTradeReview(tradeReviewTx("tx-outsider", outsider, "trade-1", 5))
	if len(tradeReviewStore.ForTrade("trade-1")) != 0 {
		t.Fatal("expected a non-participant's review to be rejected")
	}

	buildSettledP2POffer("trade-2", seller, buyer, core.SettlementAwaitingPayment)
	applyTradeReview(tradeReviewTx("tx-unsettled", buyer, "trade-2", 5))
	if len(tradeReviewStore.ForTrade("trade-2")) != 0 {
		t.Fatal("expected a review of a still-in-progress trade to be rejected")
	}
}

// TestApplyTradeReview_RejectsDoubleReviewOfSameTrade confirms the
// same reviewer can't submit two reviews for the same trade to try to
// double-count toward anything.
func TestApplyTradeReview_RejectsDoubleReviewOfSameTrade(t *testing.T) {
	resetTradeReviewTestState()

	const seller, buyer = "seller-addr", "buyer-addr"
	buildSettledP2POffer("trade-1", seller, buyer, core.SettlementConfirmed)

	applyTradeReview(tradeReviewTx("tx1", buyer, "trade-1", 5))
	applyTradeReview(tradeReviewTx("tx2", buyer, "trade-1", 1))

	reviews := tradeReviewStore.ForTrade("trade-1")
	if len(reviews) != 1 {
		t.Fatalf("expected exactly 1 review to stick, got %d", len(reviews))
	}
	if reviews[0].Stars != 5 {
		t.Fatalf("expected the FIRST review (5 stars) to be the one that stuck, got %d stars", reviews[0].Stars)
	}
}

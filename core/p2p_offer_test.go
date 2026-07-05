package core

import "testing"

const (
	testSeller    = "seller-address"
	testBuyer     = "buyer-address"
	testModerator = "moderator-address"
	testOtherAddr = "some-other-address"
)

func newExternalOffer(id string) *P2POffer {
	return &P2POffer{
		ID:               id,
		Creator:          testSeller,
		CreatorPublicKey: "seller-pub",
		OfferSymbol:      "CCT",
		OfferAmount:      100,
		WantSymbol:       "USD",
		WantAmount:       50,
		PaymentMode:      PaymentModeExternal,
		Status:           OfferOpen,
		ModeratorAddress: testModerator,
		ModerationStatus: ModerationPending,
	}
}

func storeWithOffer(o *P2POffer) *P2POfferStore {
	s := NewP2POfferStore()
	s.Create(o)
	return s
}

// --- Accept / Cancel: only ONE of them can win a race on an OPEN offer ---

func TestP2POffer_AcceptThenCancelBothFail(t *testing.T) {
	s := storeWithOffer(newExternalOffer("o1"))

	if _, ok := s.Accept("o1", testBuyer); !ok {
		t.Fatal("expected first Accept on an OPEN offer to succeed")
	}
	// The offer is no longer OPEN - a second accept or a cancel must
	// both be rejected, whichever is attempted.
	if _, ok := s.Accept("o1", "another-buyer"); ok {
		t.Fatal("expected a second Accept on an already-accepted offer to fail")
	}
	if _, ok := s.Cancel("o1"); ok {
		t.Fatal("expected Cancel on an already-accepted offer to fail")
	}
}

func TestP2POffer_CancelThenAcceptBothFail(t *testing.T) {
	s := storeWithOffer(newExternalOffer("o1"))

	if _, ok := s.Cancel("o1"); !ok {
		t.Fatal("expected Cancel on an OPEN offer to succeed")
	}
	if _, ok := s.Accept("o1", testBuyer); ok {
		t.Fatal("expected Accept on a cancelled offer to fail")
	}
}

func TestP2POffer_UnknownIDFailsEverywhere(t *testing.T) {
	s := NewP2POfferStore()
	if _, ok := s.Accept("missing", testBuyer); ok {
		t.Fatal("expected Accept on an unknown offer id to fail")
	}
	if _, ok := s.Cancel("missing"); ok {
		t.Fatal("expected Cancel on an unknown offer id to fail")
	}
}

// --- EXTERNAL settlement state machine: AWAITING_PAYMENT -> SELLER_CONFIRMED -> CONFIRMED/DISPUTED -> RESOLVED_* ---

func beginAwaitingPayment(t *testing.T, s *P2POfferStore, id string) {
	t.Helper()
	if _, ok := s.BeginExternalSettlement(id, testBuyer, "buyer-pub", 1000); !ok {
		t.Fatalf("BeginExternalSettlement on offer %s should have succeeded", id)
	}
}

func TestP2POffer_FullHappyPath_BuyerAgrees(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)

	beginAwaitingPayment(t, s, "o1")
	if o.SettlementStatus != SettlementAwaitingPayment {
		t.Fatalf("expected AWAITING_PAYMENT, got %s", o.SettlementStatus)
	}
	if o.AwaitingPaymentSince != 1000 {
		t.Fatalf("expected AwaitingPaymentSince=1000, got %d", o.AwaitingPaymentSince)
	}

	if ok := s.MarkBuyerPaid("o1", testBuyer, 1100); !ok {
		t.Fatal("expected buyer to be able to mark payment sent while AWAITING_PAYMENT")
	}
	if o.SettlementStatus != SettlementAwaitingPayment {
		t.Fatal("MarkBuyerPaid must NOT advance SettlementStatus - it's informational only")
	}

	if ok := s.ConfirmSellerPaid("o1", testSeller); !ok {
		t.Fatal("expected seller to confirm payment received")
	}
	if o.SettlementStatus != SettlementSellerConfirmed {
		t.Fatalf("expected SELLER_CONFIRMED, got %s", o.SettlementStatus)
	}

	if _, ok := s.ConfirmBuyer("o1", testBuyer, true); !ok {
		t.Fatal("expected buyer's agree-confirmation to succeed")
	}
	if o.SettlementStatus != SettlementConfirmed {
		t.Fatalf("expected CONFIRMED, got %s", o.SettlementStatus)
	}
}

func TestP2POffer_DisputePath_ThenModeratorResolves(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)

	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller)

	if _, ok := s.ConfirmBuyer("o1", testBuyer, false); !ok {
		t.Fatal("expected buyer's disagree-confirmation to succeed")
	}
	if o.SettlementStatus != SettlementDisputed {
		t.Fatalf("expected DISPUTED, got %s", o.SettlementStatus)
	}

	if _, ok := s.ResolveDispute("o1", testModerator, true); !ok {
		t.Fatal("expected the assigned moderator to resolve the dispute")
	}
	if o.SettlementStatus != SettlementResolvedRelease {
		t.Fatalf("expected RESOLVED_RELEASE, got %s", o.SettlementStatus)
	}
}

func TestP2POffer_ResolveDispute_RefundSeller(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)

	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller)
	s.ConfirmBuyer("o1", testBuyer, false)

	if _, ok := s.ResolveDispute("o1", testModerator, false); !ok {
		t.Fatal("expected the assigned moderator to resolve the dispute")
	}
	if o.SettlementStatus != SettlementResolvedRefund {
		t.Fatalf("expected RESOLVED_REFUND, got %s", o.SettlementStatus)
	}
}

// --- Authorization: only the right party can act at each step ---

func TestP2POffer_OnlyAssignedBuyerCanMarkPaid(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")

	if ok := s.MarkBuyerPaid("o1", testOtherAddr, 1100); ok {
		t.Fatal("expected a non-buyer to be rejected from marking payment paid")
	}
}

func TestP2POffer_OnlyCreatorCanConfirmSellerPaid(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")

	if ok := s.ConfirmSellerPaid("o1", testOtherAddr); ok {
		t.Fatal("expected a non-creator to be rejected from confirming payment received")
	}
}

func TestP2POffer_OnlyAssignedBuyerCanConfirm(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller)

	if _, ok := s.ConfirmBuyer("o1", testOtherAddr, true); ok {
		t.Fatal("expected a non-buyer to be rejected from answering the confirm step")
	}
}

func TestP2POffer_OnlyAssignedModeratorCanResolve(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller)
	s.ConfirmBuyer("o1", testBuyer, false)

	if _, ok := s.ResolveDispute("o1", testOtherAddr, true); ok {
		t.Fatal("expected a non-assigned moderator to be rejected from resolving")
	}
}

// --- Ordering: each step is only valid from its specific predecessor state ---

func TestP2POffer_CannotSkipSellerConfirmation(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")

	// Buyer tries to confirm before the seller has claimed payment -
	// must fail, since ConfirmBuyer requires SELLER_CONFIRMED.
	if _, ok := s.ConfirmBuyer("o1", testBuyer, true); ok {
		t.Fatal("expected ConfirmBuyer to fail before the seller has confirmed payment")
	}
}

func TestP2POffer_CannotDoubleConfirmSellerPaid(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller)

	if ok := s.ConfirmSellerPaid("o1", testSeller); ok {
		t.Fatal("expected a second ConfirmSellerPaid call to fail - it's a one-shot claim")
	}
}

func TestP2POffer_CannotResolveUndisputedOffer(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller)
	s.ConfirmBuyer("o1", testBuyer, true) // agrees -> CONFIRMED, never disputed

	if _, ok := s.ResolveDispute("o1", testModerator, true); ok {
		t.Fatal("expected ResolveDispute to fail on an offer that was never disputed")
	}
}

func TestP2POffer_OnChainOfferCannotBeginExternalSettlement(t *testing.T) {
	o := newExternalOffer("o1")
	o.PaymentMode = PaymentModeOnChain
	s := storeWithOffer(o)

	if _, ok := s.BeginExternalSettlement("o1", testBuyer, "buyer-pub", 1000); ok {
		t.Fatal("expected BeginExternalSettlement to reject an ONCHAIN-mode offer")
	}
}

// --- Messages: only while the offer is an active EXTERNAL trade ---

func TestP2POffer_AddMessage_RejectsBeforeAcceptOrForOnChain(t *testing.T) {
	openOffer := newExternalOffer("open")
	s := storeWithOffer(openOffer)
	if ok := s.AddMessage("open", NegotiationMessage{Sender: testSeller, Text: "hi"}); ok {
		t.Fatal("expected AddMessage to fail before the offer has been accepted")
	}

	onChain := newExternalOffer("onchain")
	onChain.PaymentMode = PaymentModeOnChain
	onChain.Status = OfferAccepted
	s2 := storeWithOffer(onChain)
	if ok := s2.AddMessage("onchain", NegotiationMessage{Sender: testSeller, Text: "hi"}); ok {
		t.Fatal("expected AddMessage to reject an ONCHAIN-mode offer even if accepted")
	}
}

func TestP2POffer_AddMessage_SucceedsAfterAccept(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")

	if ok := s.AddMessage("o1", NegotiationMessage{Sender: testBuyer, Text: "sending now"}); !ok {
		t.Fatal("expected AddMessage to succeed on an accepted EXTERNAL offer")
	}
	if len(o.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(o.Messages))
	}
}

// --- Moderate: one-shot attestation, only by the assigned moderator ---

func TestP2POffer_Moderate_OnlyAssignedModeratorOnce(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)

	if ok := s.Moderate("o1", testOtherAddr, false); ok {
		t.Fatal("expected Moderate to reject a non-assigned moderator")
	}
	if ok := s.Moderate("o1", testModerator, false); !ok {
		t.Fatal("expected the assigned moderator's first attestation to succeed")
	}
	if o.ModerationStatus != ModerationConfirmed {
		t.Fatalf("expected CONFIRMED, got %s", o.ModerationStatus)
	}
	if ok := s.Moderate("o1", testModerator, true); ok {
		t.Fatal("expected a second Moderate call to fail - it's one-shot")
	}
}

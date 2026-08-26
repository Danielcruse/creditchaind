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

	if ok := s.ConfirmSellerPaid("o1", testSeller, 1200); !ok {
		t.Fatal("expected seller to confirm payment received")
	}
	if o.SettlementStatus != SettlementConfirmed {
		t.Fatalf("expected CONFIRMED immediately (no separate buyer confirm step anymore), got %s", o.SettlementStatus)
	}
	if o.SellerConfirmedAt != 1200 {
		t.Fatalf("expected SellerConfirmedAt=1200, got %d", o.SellerConfirmedAt)
	}
}

func TestP2POffer_DisputePath_ThenModeratorResolves(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)

	beginAwaitingPayment(t, s, "o1")
	if ok := s.MarkBuyerPaid("o1", testBuyer, 1100); !ok {
		t.Fatal("expected buyer to mark payment sent")
	}

	// Seller disputes the claim BEFORE ever confirming it - the new
	// dispute entry point, replacing the old post-confirm buyer
	// disagreement path.
	if _, ok := s.DisputeAwaitingPayment("o1", testSeller); !ok {
		t.Fatal("expected seller's dispute to succeed")
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
	s.MarkBuyerPaid("o1", testBuyer, 1100)
	s.DisputeAwaitingPayment("o1", testSeller)

	if _, ok := s.ResolveDispute("o1", testModerator, false); !ok {
		t.Fatal("expected the assigned moderator to resolve the dispute")
	}
	if o.SettlementStatus != SettlementResolvedRefund {
		t.Fatalf("expected RESOLVED_REFUND, got %s", o.SettlementStatus)
	}
}

// --- Buyer's own escalation path (DisputeAwaitingPaymentByBuyer) ---

func TestP2POffer_BuyerDispute_TooEarlyFails(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.MarkBuyerPaid("o1", testBuyer, 1100)

	// One second short of the window - must still fail.
	if _, ok := s.DisputeAwaitingPaymentByBuyer("o1", testBuyer, 1100+DisputeEligibleAfterSeconds-1); ok {
		t.Fatal("expected buyer dispute to fail before DisputeEligibleAfterSeconds has elapsed")
	}
	if o.SettlementStatus != SettlementAwaitingPayment {
		t.Fatalf("expected status to remain AWAITING_PAYMENT, got %s", o.SettlementStatus)
	}
}

func TestP2POffer_BuyerDispute_AfterWindowSucceeds(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.MarkBuyerPaid("o1", testBuyer, 1100)

	if _, ok := s.DisputeAwaitingPaymentByBuyer("o1", testBuyer, 1100+DisputeEligibleAfterSeconds); !ok {
		t.Fatal("expected buyer dispute to succeed once DisputeEligibleAfterSeconds has elapsed")
	}
	if o.SettlementStatus != SettlementDisputed {
		t.Fatalf("expected DISPUTED, got %s", o.SettlementStatus)
	}

	// The moderator can resolve it exactly like a seller-initiated
	// dispute - DisputeAwaitingPaymentByBuyer lands on the same
	// terminal-pending status ResolveDispute already knows how to
	// adjudicate.
	if _, ok := s.ResolveDispute("o1", testModerator, true); !ok {
		t.Fatal("expected the assigned moderator to resolve the buyer-initiated dispute")
	}
	if o.SettlementStatus != SettlementResolvedRelease {
		t.Fatalf("expected RESOLVED_RELEASE, got %s", o.SettlementStatus)
	}
}

func TestP2POffer_BuyerDispute_WrongCallerFails(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.MarkBuyerPaid("o1", testBuyer, 1100)

	if _, ok := s.DisputeAwaitingPaymentByBuyer("o1", testOtherAddr, 1100+DisputeEligibleAfterSeconds); ok {
		t.Fatal("expected a non-buyer to be rejected from the buyer's own dispute path")
	}
	// The seller itself must also be rejected here - this is
	// specifically the BUYER's escalation path; the seller already has
	// its own immediate DisputeAwaitingPayment.
	if _, ok := s.DisputeAwaitingPaymentByBuyer("o1", testSeller, 1100+DisputeEligibleAfterSeconds); ok {
		t.Fatal("expected the seller to be rejected from the buyer's own dispute path")
	}
}

func TestP2POffer_BuyerDispute_NoPaymentClaimFails(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")

	// Buyer never called MarkBuyerPaid - nothing to escalate.
	if _, ok := s.DisputeAwaitingPaymentByBuyer("o1", testBuyer, 1000+DisputeEligibleAfterSeconds); ok {
		t.Fatal("expected buyer dispute to fail when no payment claim is on record")
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

	if ok := s.ConfirmSellerPaid("o1", testOtherAddr, 1200); ok {
		t.Fatal("expected a non-creator to be rejected from confirming payment received")
	}
}

// TestP2POffer_OnlyAssignedBuyerCanConfirm exercises ConfirmBuyer's own
// authorization check directly against a manually-constructed
// SELLER_CONFIRMED offer - ConfirmSellerPaid itself no longer produces
// this status for new trades (see its doc comment), but ConfirmBuyer is
// kept working for any offer already sitting there, so its
// authorization behavior still needs to be correct.
func TestP2POffer_OnlyAssignedBuyerCanConfirm(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	o.SettlementStatus = SettlementSellerConfirmed

	if _, ok := s.ConfirmBuyer("o1", testOtherAddr, true); ok {
		t.Fatal("expected a non-buyer to be rejected from answering the confirm step")
	}
}

func TestP2POffer_OnlyAssignedModeratorCanResolve(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.MarkBuyerPaid("o1", testBuyer, 1100)
	s.DisputeAwaitingPayment("o1", testSeller)

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
	s.ConfirmSellerPaid("o1", testSeller, 1200)

	if ok := s.ConfirmSellerPaid("o1", testSeller, 1200); ok {
		t.Fatal("expected a second ConfirmSellerPaid call to fail - it's a one-shot claim")
	}
}

func TestP2POffer_CannotResolveUndisputedOffer(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.ConfirmSellerPaid("o1", testSeller, 1200) // -> CONFIRMED directly, never disputed

	if _, ok := s.ResolveDispute("o1", testModerator, true); ok {
		t.Fatal("expected ResolveDispute to fail on an offer that was never disputed")
	}
}

// --- DisputeAwaitingPayment: the seller's new pre-confirm dispute path ---

func TestP2POffer_DisputeAwaitingPayment_RequiresBuyerClaimFirst(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")

	if _, ok := s.DisputeAwaitingPayment("o1", testSeller); ok {
		t.Fatal("expected dispute to fail before the buyer has claimed payment")
	}
}

func TestP2POffer_DisputeAwaitingPayment_OnlyCreatorCanDispute(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	s.MarkBuyerPaid("o1", testBuyer, 1100)

	if _, ok := s.DisputeAwaitingPayment("o1", testOtherAddr); ok {
		t.Fatal("expected a non-creator to be rejected from disputing")
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

// --- SweepStalledSettlements: the three ways a stalled trade closes ---

func fixedWindowMinutes(minutes int) func(o *P2POffer) int {
	return func(o *P2POffer) int { return minutes }
}

func TestP2POffer_Sweep_UnattendedExpiresAndRefundsSeller(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1") // AwaitingPaymentSince = 1000, neither side has acted

	// Still within the window - nothing should happen yet.
	stalled := s.SweepStalledSettlements(1000+30*60-1, fixedWindowMinutes(30))
	if len(stalled) != 0 {
		t.Fatalf("expected no stalled offers before the window elapses, got %d", len(stalled))
	}
	if o.SettlementStatus != SettlementAwaitingPayment {
		t.Fatalf("expected AWAITING_PAYMENT unchanged, got %s", o.SettlementStatus)
	}

	// Window has now passed with neither party acting.
	stalled = s.SweepStalledSettlements(1000+30*60+1, fixedWindowMinutes(30))
	if len(stalled) != 1 || stalled[0].Offer.ID != "o1" {
		t.Fatalf("expected offer o1 to be swept as stalled, got %+v", stalled)
	}
	if stalled[0].ReleaseToBuyer {
		t.Fatal("expected unattended-by-both-parties to refund the seller, not release to buyer")
	}
	if o.SettlementStatus != SettlementExpired {
		t.Fatalf("expected EXPIRED, got %s", o.SettlementStatus)
	}

	// A second sweep must not re-match an already-terminal offer.
	if stalled := s.SweepStalledSettlements(1000+100*60, fixedWindowMinutes(30)); len(stalled) != 0 {
		t.Fatalf("expected an already-EXPIRED offer not to be swept again, got %d", len(stalled))
	}
}

func TestP2POffer_Sweep_BuyerMarkedPaidAutoReleasesAfterOneHour(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	if ok := s.MarkBuyerPaid("o1", testBuyer, 1000); !ok {
		t.Fatal("expected MarkBuyerPaid to succeed")
	}

	// Just under an hour since the buyer marked paid - seller still has time.
	if stalled := s.SweepStalledSettlements(1000+3599, fixedWindowMinutes(30)); len(stalled) != 0 {
		t.Fatalf("expected no auto-release before a full hour has passed, got %d", len(stalled))
	}

	// A full hour has passed with the seller never confirming.
	stalled := s.SweepStalledSettlements(1000+3600, fixedWindowMinutes(30))
	if len(stalled) != 1 || !stalled[0].ReleaseToBuyer {
		t.Fatalf("expected offer to auto-release to the buyer, got %+v", stalled)
	}
	if o.SettlementStatus != SettlementAutoReleased {
		t.Fatalf("expected AUTO_RELEASED, got %s", o.SettlementStatus)
	}
}

// TestP2POffer_Sweep_SellerConfirmedAutoReleasesAfterOneHourOfBuyerSilence
// exercises the legacy SELLER_CONFIRMED sweep branch directly (see
// SweepStalledSettlements' doc comment) - ConfirmSellerPaid itself no
// longer produces this status for new trades, but any offer already
// sitting there needs this safety net to still work.
func TestP2POffer_Sweep_SellerConfirmedAutoReleasesAfterOneHourOfBuyerSilence(t *testing.T) {
	o := newExternalOffer("o1")
	s := storeWithOffer(o)
	beginAwaitingPayment(t, s, "o1")
	o.SettlementStatus = SettlementSellerConfirmed
	o.SellerConfirmedAt = 2000

	if stalled := s.SweepStalledSettlements(2000+3599, fixedWindowMinutes(30)); len(stalled) != 0 {
		t.Fatalf("expected no auto-release before a full hour has passed, got %d", len(stalled))
	}

	stalled := s.SweepStalledSettlements(2000+3600, fixedWindowMinutes(30))
	if len(stalled) != 1 || !stalled[0].ReleaseToBuyer {
		t.Fatalf("expected offer to auto-release to the buyer, got %+v", stalled)
	}
	if o.SettlementStatus != SettlementAutoReleased {
		t.Fatalf("expected AUTO_RELEASED, got %s", o.SettlementStatus)
	}
}

func TestP2POffer_Sweep_IgnoresOnChainAndTerminalOffers(t *testing.T) {
	onchain := newExternalOffer("o1")
	onchain.PaymentMode = PaymentModeOnChain
	onchain.SettlementStatus = ""

	s := storeWithOffer(onchain)
	confirmed := newExternalOffer("o2")
	confirmed.SettlementStatus = SettlementConfirmed
	s.Create(confirmed)

	if stalled := s.SweepStalledSettlements(1_000_000_000, fixedWindowMinutes(30)); len(stalled) != 0 {
		t.Fatalf("expected ONCHAIN and already-terminal offers to never be swept, got %d", len(stalled))
	}
}

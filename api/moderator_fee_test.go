package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestP2PDisputeFlow_TracksUptimeAndPaysResolvingModeratorDirectly is
// the end-to-end regression test for this session's moderator-funding
// redesign: opening a dispute records it against the assigned
// moderator's DisputesAssigned, and resolving it as a release both
// records DisputesResolved AND pays that moderator directly from the
// trade's platform fee - never the pooled address, since this is real
// work someone actually did (see splitPlatformFee's own doc comment).
func TestP2PDisputeFlow_TracksUptimeAndPaysResolvingModeratorDirectly(t *testing.T) {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()

	const seller, buyer, moderator = "seller-addr", "buyer-addr", "resolving-moderator"
	moderatorStore.Register(moderator, "mod-pub", 1000)
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const offerID = "dispute-flow-offer"
	p2pOfferStore.Create(&core.P2POffer{
		ID:               offerID,
		Creator:          seller,
		CreatorPublicKey: "seller-pub",
		OfferSymbol:      "CCT",
		OfferAmount:      100_000, // large enough that the moderator's 10% fee share floors to a nonzero amount
		WantSymbol:       "USD",
		WantAmount:       500,
		PaymentMode:      core.PaymentModeExternal,
		Status:           core.OfferOpen,
		ModeratorAddress: moderator,
		ModerationStatus: core.ModerationPending,
	})
	if _, ok := p2pOfferStore.Accept(offerID, buyer); !ok {
		t.Fatal("failed to accept offer as buyer")
	}
	offer, _ := p2pOfferStore.Get(offerID)
	offer.SettlementStatus = core.SettlementAwaitingPayment
	offer.AwaitingPaymentSince = 1000
	offer.BuyerMarkedPaidAt = 1000
	cctStore.Mint(p2pOfferEscrowAddress(offerID), cctW(offer.OfferAmount))

	// Buyer escalates - eligible once DisputeEligibleAfterSeconds has
	// passed since BuyerMarkedPaidAt.
	disputeData, _ := json.Marshal(p2pBuyerDisputeData{OfferID: offerID})
	disputeTx := core.Transaction{
		ID: "dispute-tx", From: buyer, Type: core.TxP2PBuyerDispute,
		Data: string(disputeData), Timestamp: 1000 + core.DisputeEligibleAfterSeconds,
	}
	applyP2PBuyerDispute(disputeTx)

	modStatus, ok := moderatorStore.Get(moderator)
	if !ok || modStatus.DisputesAssigned != 1 {
		t.Fatalf("expected DisputesAssigned=1 after the dispute opened, got %+v", modStatus)
	}
	if got := modStatus.Uptime(); got != 0 {
		t.Fatalf("expected uptime 0 before this moderator has resolved anything, got %v", got)
	}

	// Moderator resolves in the buyer's favor (release) - a genuinely
	// completed trade, so it collects the normal platform fee, paid
	// straight to the resolving moderator.
	resolveData, _ := json.Marshal(p2pModeratorResolveData{OfferID: offerID, Release: true})
	resolveTx := core.Transaction{
		ID: "resolve-tx", From: moderator, Type: core.TxP2PModeratorResolve,
		Data: string(resolveData), Timestamp: 1000 + core.DisputeEligibleAfterSeconds + 100,
	}
	applyP2PModeratorResolve(resolveTx)

	modStatus, _ = moderatorStore.Get(moderator)
	if modStatus.DisputesResolved != 1 {
		t.Fatalf("expected DisputesResolved=1 after resolving, got %d", modStatus.DisputesResolved)
	}
	if got := modStatus.Uptime(); got != 1.0 {
		t.Fatalf("expected uptime 1.0 (1 resolved / 1 assigned), got %v", got)
	}

	// Platform fee on a 100,000-CCT trade at 0.3% = 300 CCT; moderator's
	// 10% share = 30 CCT, comfortably nonzero and directly checkable.
	if got := cctStore.BalanceOf(platformFeeModeratorPoolAddress("CCT")); got != "0" {
		t.Fatalf("expected nothing pooled - this dispute's moderator share should have paid the resolver directly, got %s", got)
	}
	modBalance := cctStore.BalanceOf(moderator)
	if modBalance == "0" {
		t.Fatal("expected the resolving moderator to have received a direct fee payment, got 0")
	}
}

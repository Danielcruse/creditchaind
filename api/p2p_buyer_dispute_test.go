package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// newAwaitingPaymentOfferWithBuyerClaim seeds a P2POffer already in the
// exact state HandleP2PBuyerDispute cares about: AWAITING_PAYMENT, with
// a real buyer signed up and a payment claim on record at
// markedPaidAt. Mirrors core/p2p_offer_test.go's own
// newExternalOffer/beginAwaitingPayment setup, just at the HTTP-handler
// layer instead of calling the store methods directly.
func newAwaitingPaymentOfferWithBuyerClaim(t *testing.T, id, seller, buyer string, markedPaidAt int64) {
	t.Helper()
	p2pOfferStore.Create(&core.P2POffer{
		ID:               id,
		Creator:          seller,
		CreatorPublicKey: "seller-pub",
		OfferSymbol:      "CCT",
		OfferAmount:      100,
		WantSymbol:       "USD",
		WantAmount:       50,
		PaymentMode:      core.PaymentModeExternal,
		Status:           core.OfferOpen,
		ModeratorAddress: "moderator-address",
		ModerationStatus: core.ModerationPending,
	})
	if _, ok := p2pOfferStore.Accept(id, buyer); !ok {
		t.Fatalf("failed to accept offer %s as buyer", id)
	}
	offer, ok := p2pOfferStore.Get(id)
	if !ok {
		t.Fatalf("offer %s not found after accept", id)
	}
	offer.SettlementStatus = core.SettlementAwaitingPayment
	offer.AwaitingPaymentSince = markedPaidAt
	offer.BuyerMarkedPaidAt = markedPaidAt
}

func TestHandleP2PBuyerDispute_NonBuyerRejected(t *testing.T) {
	resetHubOperatorTestState()

	sellerPub, _, _ := core.GenerateKeyPair()
	sellerAddr := core.GenerateAddress(sellerPub)
	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)
	strangerPub, strangerPriv, _ := core.GenerateKeyPair()

	newAwaitingPaymentOfferWithBuyerClaim(t, "o1", sellerAddr, buyerAddr, 1000)
	_ = buyerPriv

	body := buildSignedEnvelopeJSON(t, strangerPub, strangerPriv, p2pBuyerDisputeData{OfferID: "o1"})
	req := httptest.NewRequest(http.MethodPost, "/p2p/offers/buyer-dispute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleP2PBuyerDispute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-buyer caller, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleP2PBuyerDispute_TooEarlyRejected(t *testing.T) {
	resetHubOperatorTestState()

	sellerPub, _, _ := core.GenerateKeyPair()
	sellerAddr := core.GenerateAddress(sellerPub)
	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)

	// Marked paid "now" - well within the DisputeEligibleAfterSeconds
	// window, so this must be rejected regardless of anything else.
	newAwaitingPaymentOfferWithBuyerClaim(t, "o1", sellerAddr, buyerAddr, time.Now().Unix())

	body := buildSignedEnvelopeJSON(t, buyerPub, buyerPriv, p2pBuyerDisputeData{OfferID: "o1"})
	req := httptest.NewRequest(http.MethodPost, "/p2p/offers/buyer-dispute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleP2PBuyerDispute(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 before the dispute window has elapsed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleP2PBuyerDispute_EligibleSucceeds(t *testing.T) {
	resetHubOperatorTestState()

	sellerPub, _, _ := core.GenerateKeyPair()
	sellerAddr := core.GenerateAddress(sellerPub)
	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)

	// Marked paid well over DisputeEligibleAfterSeconds ago.
	newAwaitingPaymentOfferWithBuyerClaim(t, "o1", sellerAddr, buyerAddr, time.Now().Unix()-core.DisputeEligibleAfterSeconds-60)

	body := buildSignedEnvelopeJSON(t, buyerPub, buyerPriv, p2pBuyerDisputeData{OfferID: "o1"})
	req := httptest.NewRequest(http.MethodPost, "/p2p/offers/buyer-dispute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleP2PBuyerDispute(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 once the dispute window has elapsed, got %d: %s", rec.Code, rec.Body.String())
	}
}

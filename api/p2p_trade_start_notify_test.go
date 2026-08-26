package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// buildP2PExternalAcceptTx builds a genuinely wallet-signed
// TxP2PExternalAccept transaction, mirroring buildSignedTransferTx's
// pattern elsewhere in this package.
func buildP2PExternalAcceptTx(t *testing.T, offerID string, ts int64) (core.Transaction, string) {
	t.Helper()
	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	payload := p2pExternalAcceptData{OfferID: offerID}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	env := core.SignedEnvelope{Address: from, PublicKey: hex.EncodeToString(pub), Timestamp: ts, Payload: payloadJSON}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	return core.Transaction{
		ID:            "p2p-ext-accept-" + from,
		From:          from,
		Type:          core.TxP2PExternalAccept,
		Data:          string(payloadJSON),
		Timestamp:     ts,
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}, from
}

// TestApplyP2PExternalAccept_StartsSettlementOnFirstAccept confirms the
// happy path still works after adding the success check around
// BeginExternalSettlement - a real accept must still move the offer
// into AWAITING_PAYMENT.
func TestApplyP2PExternalAccept_StartsSettlementOnFirstAccept(t *testing.T) {
	resetHubOperatorTestState()

	sellerPub, _, _ := core.GenerateKeyPair()
	sellerAddr := core.GenerateAddress(sellerPub)
	p2pOfferStore.Create(&core.P2POffer{
		ID: "offer-start-1", Creator: sellerAddr, CreatorPublicKey: "seller-pub",
		OfferSymbol: "CCT", OfferAmount: 100, WantSymbol: "USD", WantAmount: 50,
		PaymentMode: core.PaymentModeExternal, Status: core.OfferOpen,
	})

	tx, buyerAddr := buildP2PExternalAcceptTx(t, "offer-start-1", 1000)
	applyP2PExternalAccept(tx)

	offer, ok := p2pOfferStore.Get("offer-start-1")
	if !ok {
		t.Fatalf("offer missing after accept")
	}
	if offer.Status != core.OfferAccepted || offer.SettlementStatus != core.SettlementAwaitingPayment {
		t.Fatalf("expected ACCEPTED/AWAITING_PAYMENT, got status=%s settlement=%s", offer.Status, offer.SettlementStatus)
	}
	if offer.AcceptedBy != buyerAddr {
		t.Fatalf("expected AcceptedBy=%s, got %s", buyerAddr, offer.AcceptedBy)
	}
}

// TestApplyP2PExternalAccept_SecondAcceptIsANoOp is a regression test
// for the bug this session's fix addressed: BeginExternalSettlement's
// bool return value used to be silently discarded, so a second accept
// on an already-accepted offer would fall through to
// notifyTradeStarted (and, before that fix existed, nothing would catch
// that the second "accept" did nothing at the store level either -
// this confirms the offer's AcceptedBy stays the FIRST acceptor, never
// silently overwritten or double-processed).
func TestApplyP2PExternalAccept_SecondAcceptIsANoOp(t *testing.T) {
	resetHubOperatorTestState()

	sellerPub, _, _ := core.GenerateKeyPair()
	sellerAddr := core.GenerateAddress(sellerPub)
	p2pOfferStore.Create(&core.P2POffer{
		ID: "offer-start-2", Creator: sellerAddr, CreatorPublicKey: "seller-pub",
		OfferSymbol: "CCT", OfferAmount: 100, WantSymbol: "USD", WantAmount: 50,
		PaymentMode: core.PaymentModeExternal, Status: core.OfferOpen,
	})

	firstTx, firstBuyer := buildP2PExternalAcceptTx(t, "offer-start-2", 1000)
	applyP2PExternalAccept(firstTx)

	secondTx, secondBuyer := buildP2PExternalAcceptTx(t, "offer-start-2", 1001)
	applyP2PExternalAccept(secondTx)

	offer, ok := p2pOfferStore.Get("offer-start-2")
	if !ok {
		t.Fatalf("offer missing")
	}
	if offer.AcceptedBy != firstBuyer {
		t.Fatalf("expected the offer to stay accepted by the first buyer %s, got %s (second buyer was %s)", firstBuyer, offer.AcceptedBy, secondBuyer)
	}
}

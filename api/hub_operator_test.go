package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetHubOperatorTestState resets every package-level store the
// multi-operator marketplace path touches, on top of the plain-
// transfer state resetApplyTestState already clears.
func resetHubOperatorTestState() {
	resetApplyTestState()
	companyStore = core.NewCompanyStore()
	hubWorkerStore = core.NewHubWorkerStore()
	hubRateStore = core.NewHubRateStore()
	p2pOfferStore = core.NewP2POfferStore()
}

// signedHubTxAs builds a genuinely-signed transaction of the given
// type for a specific, already-known
// keypair - needed so a worker/operator's rate-set/trade-request tx is
// signed with the SAME key already registered as that operator's
// WorkerPublicKey/address, rather than a fresh random one each call.
func signedHubTxAs(t *testing.T, pub []byte, priv []byte, txType core.TransactionType, payload any, ts int64) core.Transaction {
	t.Helper()

	from := core.GenerateAddress(pub)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	return core.Transaction{
		ID:            "test-tx-" + string(txType) + "-" + from + "-" + hex.EncodeToString(sig)[:8],
		From:          from,
		Type:          txType,
		Timestamp:     ts,
		Data:          string(payloadJSON),
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
}

// TestHubOperator_OwnListingAndOperatorListingAreIndependent is the
// core regression test for the multi-operator marketplace feature: a
// verified hub's own flagship rate and one of its active operator's
// own rate for the SAME symbol must coexist as two separate records,
// and a trade against either must only ever move that specific
// listing's own inventory - never the other's.
func TestHubOperator_OwnListingAndOperatorListingAreIndependent(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Status:    core.CompanyVerified,
	})

	operatorPub, operatorPriv, _ := core.GenerateKeyPair()
	operatorAddr := core.GenerateAddress(operatorPub)
	hubWorkerStore.Add(&core.HubWorker{
		HubAddress:      hubAddr,
		WorkerAddress:   operatorAddr,
		WorkerPublicKey: hex.EncodeToString(operatorPub),
		Active:          true,
	})

	// Hub posts its own flagship rate (Hub "" defaults to the signer -
	// see authorizedHubActor).
	hubRateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "NGN", SellRate: 2, Active: true,
	}, 1000)
	applyTxOnce(hubRateTx)

	// Operator posts their OWN independent rate for the same symbol
	// under this hub.
	operatorRateTx := signedHubTxAs(t, operatorPub, operatorPriv, core.TxHubRateSet, hubRateSetData{
		Hub: hubAddr, Symbol: "CCT", FiatSymbol: "NGN", SellRate: 5, Active: true,
	}, 1000)
	applyTxOnce(operatorRateTx)

	hubRate, ok := hubRateStore.Get(hubAddr, "CCT", "", "NGN")
	if !ok {
		t.Fatalf("expected the hub's own flagship rate to exist")
	}
	if hubRate.SellRate != 2 || hubRate.OperatorAddress != "" {
		t.Fatalf("hub's own rate wrong: %+v", hubRate)
	}
	opRate, ok := hubRateStore.Get(hubAddr, "CCT", operatorAddr, "NGN")
	if !ok {
		t.Fatalf("expected the operator's own independent rate to exist")
	}
	if opRate.SellRate != 5 || opRate.OperatorAddress != operatorAddr {
		t.Fatalf("operator's own rate wrong: %+v", opRate)
	}
	if opRate.InventoryAddress() != operatorAddr {
		t.Fatalf("expected operator rate's InventoryAddress to be the operator, got %s", opRate.InventoryAddress())
	}
	if hubRate.InventoryAddress() != hubAddr {
		t.Fatalf("expected hub rate's InventoryAddress to be the hub, got %s", hubRate.InventoryAddress())
	}

	// Fund both inventories independently.
	cctStore.Mint(hubAddr, cctW(1000))
	cctStore.Mint(operatorAddr, cctW(1000))

	// A buyer trades against the HUB's own listing.
	buyerPub1, buyerPriv1, _ := core.GenerateKeyPair()
	tradeTx1 := signedHubTxAs(t, buyerPub1, buyerPriv1, core.TxHubTradeRequest, hubTradeRequestData{
		Hub: hubAddr, Symbol: "CCT", Amount: 100, Operator: "", FiatSymbol: "NGN", Direction: "BUY_FROM_HUB",
	}, 2000)
	applyTxOnce(tradeTx1)

	if got := cctStore.BalanceOf(hubAddr); got != cctW(900) {
		t.Fatalf("expected hub's own balance debited to 900, got %s", got)
	}
	if got := cctStore.BalanceOf(operatorAddr); got != cctW(1000) {
		t.Fatalf("expected operator's balance untouched by a trade against the HUB's own listing, got %s", got)
	}

	// A second buyer trades against the OPERATOR's own listing.
	buyerPub2, buyerPriv2, _ := core.GenerateKeyPair()
	tradeTx2 := signedHubTxAs(t, buyerPub2, buyerPriv2, core.TxHubTradeRequest, hubTradeRequestData{
		Hub: hubAddr, Symbol: "CCT", Amount: 100, Operator: operatorAddr, FiatSymbol: "NGN", Direction: "BUY_FROM_HUB",
	}, 3000)
	applyTxOnce(tradeTx2)

	if got := cctStore.BalanceOf(operatorAddr); got != cctW(900) {
		t.Fatalf("expected operator's own balance debited to 900, got %s", got)
	}
	if got := cctStore.BalanceOf(hubAddr); got != cctW(900) {
		t.Fatalf("expected hub's own balance untouched by a trade against the OPERATOR's listing, got %s", got)
	}

	offer2, ok := p2pOfferStore.Get(tradeTx2.ID)
	if !ok {
		t.Fatalf("expected the operator trade to create a P2POffer")
	}
	if offer2.Creator != operatorAddr {
		t.Fatalf("expected operator trade's Creator to be the operator (not the hub), got %s", offer2.Creator)
	}
	if offer2.HubAddress != hubAddr {
		t.Fatalf("expected operator trade to still be tagged with the parent hub's address, got %s", offer2.HubAddress)
	}
}

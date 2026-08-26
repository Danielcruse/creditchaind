package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestHandleCreatePurchaseRequest_RejectsInvalidMethodAndMissingEmail(t *testing.T) {
	resetApplyTestState()

	traderPub, traderPriv, _ := core.GenerateKeyPair()

	badMethodBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, treasuryPurchaseRequestData{
		CCTAmount: 10, Method: "CARD_PAYMENT", BuyerEmail: "buyer@example.com",
	})
	badMethodReq := httptest.NewRequest(http.MethodPost, "/treasury/purchase-request", bytes.NewReader(badMethodBody))
	badMethodRec := httptest.NewRecorder()
	HandleCreatePurchaseRequest(badMethodRec, badMethodReq)
	if badMethodRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid method (e.g. card payment), got %d: %s", badMethodRec.Code, badMethodRec.Body.String())
	}

	noEmailBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, treasuryPurchaseRequestData{
		CCTAmount: 10, Method: core.PurchaseMethodBankTransfer, BuyerEmail: "",
	})
	noEmailReq := httptest.NewRequest(http.MethodPost, "/treasury/purchase-request", bytes.NewReader(noEmailBody))
	noEmailRec := httptest.NewRecorder()
	HandleCreatePurchaseRequest(noEmailRec, noEmailReq)
	if noEmailRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing buyer email, got %d: %s", noEmailRec.Code, noEmailRec.Body.String())
	}
}

// TestTreasuryPurchase_FullInstructionsToConfirmFlow drives the whole
// feature end to end through the real HTTP handlers plus the apply
// functions they queue for (mirroring how a confirmed block would
// actually invoke them): admin sends instructions, buyer confirms
// payment with the right token, admin confirms, CCT actually moves.
func TestTreasuryPurchase_FullInstructionsToConfirmFlow(t *testing.T) {
	resetApplyTestState()

	foundation := "foundation-address"
	treasuryStore.SetFoundationAddress(foundation)
	cctStore.Mint(foundation, cctW(1000))

	buyerPub, _, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)

	treasuryStore.CreatePurchase(&core.PurchaseRequest{
		ID: "purchase-1", BuyerAddress: buyerAddr, CCTAmount: 100,
		Method: core.PurchaseMethodBankTransfer, BuyerEmail: "buyer@example.com",
		Status: core.PurchaseAwaitingPayment, CreatedAt: 1000,
	})

	// --- Admin sends instructions ---
	adminEnv, adminAddr := adminSignedEnvelope(t, adminSendPurchaseInstructionsData{
		PurchaseID: "purchase-1", Instructions: "Bank: Foo Bank, Acct 999888777",
	})
	t.Setenv("ADMIN_WALLET_ADDRESS", adminAddr)
	sendBody, _ := json.Marshal(adminEnv)
	sendReq := httptest.NewRequest(http.MethodPost, "/admin/treasury/purchase/send-instructions", bytes.NewReader(sendBody))
	sendRec := httptest.NewRecorder()
	HandleAdminSendPurchaseInstructions(sendRec, sendReq)
	if sendRec.Code != http.StatusOK {
		t.Fatalf("expected 200 sending instructions, got %d: %s", sendRec.Code, sendRec.Body.String())
	}

	// Apply what the handler queued, exactly as a confirmed block would -
	// using a test-chosen token in place of the real handler-generated
	// one, since that token only ever leaves this node via email and
	// isn't observable from the HTTP response by design.
	purchase, _ := treasuryStore.GetPurchase("purchase-1")
	if purchase.Status != core.PurchaseAwaitingPayment {
		t.Fatalf("expected status unchanged until the tx applies, got %v", purchase.Status)
	}
	const testToken = "test-fixed-confirm-token"
	applyTreasuryPurchaseSendInstructions(core.Transaction{
		Data: mustMarshal(t, treasuryPurchaseSendInstructionsData{
			adminSendPurchaseInstructionsData: adminSendPurchaseInstructionsData{PurchaseID: "purchase-1", Instructions: "Bank: Foo Bank, Acct 999888777"},
			Token:                             testToken,
		}),
	})

	purchase, _ = treasuryStore.GetPurchase("purchase-1")
	if purchase.Status != core.PurchaseInstructionsSent {
		t.Fatalf("expected INSTRUCTIONS_SENT after apply, got %v", purchase.Status)
	}
	token := purchase.ConfirmToken
	if token == "" {
		t.Fatalf("expected a non-empty confirm token after instructions were sent")
	}

	// --- Buyer clicks the confirm link with the WRONG token first ---
	wrongReq := httptest.NewRequest(http.MethodGet, "/treasury/purchase/confirm-payment?id=purchase-1&token=wrong", nil)
	wrongRec := httptest.NewRecorder()
	HandleBuyerConfirmPurchasePayment(wrongRec, wrongReq)
	if wrongRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for the wrong token, got %d", wrongRec.Code)
	}
	if purchase, _ := treasuryStore.GetPurchase("purchase-1"); purchase.Status != core.PurchaseInstructionsSent {
		t.Fatalf("expected status unchanged after a wrong-token attempt, got %v", purchase.Status)
	}

	// --- Buyer clicks the confirm link with the RIGHT token ---
	rightReq := httptest.NewRequest(http.MethodGet, "/treasury/purchase/confirm-payment?id=purchase-1&token="+token, nil)
	rightRec := httptest.NewRecorder()
	HandleBuyerConfirmPurchasePayment(rightRec, rightReq)
	if rightRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the right token, got %d: %s", rightRec.Code, rightRec.Body.String())
	}
	if !strings.Contains(rightRec.Body.String(), "Thanks") {
		t.Fatalf("expected a success page, got: %s", rightRec.Body.String())
	}

	// Apply the node-authored tx the handler queued.
	applyTreasuryPurchaseBuyerMarkedPaid(core.Transaction{
		Data: mustMarshal(t, treasuryPurchaseBuyerMarkedPaidData{PurchaseID: "purchase-1", Token: token}),
	})

	purchase, _ = treasuryStore.GetPurchase("purchase-1")
	if purchase.Status != core.PurchaseBuyerMarkedPaid {
		t.Fatalf("expected BUYER_MARKED_PAID after apply, got %v", purchase.Status)
	}

	// --- Admin confirms, CCT actually moves ---
	confirmEnv, confirmAdminAddr := adminSignedEnvelope(t, treasuryPurchaseConfirmData{PurchaseID: "purchase-1"})
	t.Setenv("ADMIN_WALLET_ADDRESS", confirmAdminAddr)
	confirmBody, _ := json.Marshal(confirmEnv)
	confirmReq := httptest.NewRequest(http.MethodPost, "/admin/treasury/purchase/confirm", bytes.NewReader(confirmBody))
	confirmRec := httptest.NewRecorder()
	HandleAdminConfirmPurchase(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected 200 confirming, got %d: %s", confirmRec.Code, confirmRec.Body.String())
	}

	applyTreasuryPurchaseConfirm(core.Transaction{
		Data: mustMarshal(t, treasuryPurchaseConfirmData{PurchaseID: "purchase-1"}),
	})

	purchase, _ = treasuryStore.GetPurchase("purchase-1")
	if purchase.Status != core.PurchaseConfirmed {
		t.Fatalf("expected CONFIRMED after apply, got %v", purchase.Status)
	}
	if got := cctStore.BalanceOf(buyerAddr); got != cctW(100) {
		t.Fatalf("expected buyer to receive 100 CCT, got %s", got)
	}
	if got := cctStore.BalanceOf(foundation); got != cctW(900) {
		t.Fatalf("expected foundation balance reduced to 900 CCT, got %s", got)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

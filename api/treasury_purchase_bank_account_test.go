package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleCreatePurchaseRequest_BankTransferRequiresConfiguredCurrency
// is a regression test for the new preset-bank-account flow: a buyer
// picking BANK_TRANSFER for a currency the admin hasn't configured yet
// must get a clear rejection, not a purchase request that later has
// nowhere to show payment details.
func TestHandleCreatePurchaseRequest_BankTransferRequiresConfiguredCurrency(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "foundation-bt-1"
	cctStore.Mint("foundation-bt-1", cctW(1000))

	pub, priv, _ := core.GenerateKeyPair()

	body := buildSignedEnvelopeJSON(t, pub, priv, treasuryPurchaseRequestData{
		CCTAmount: 10, Method: core.PurchaseMethodBankTransfer, BuyerEmail: "buyer@example.com",
		BuyerName: "Jane Buyer", Currency: "USD",
	})
	req := httptest.NewRequest(http.MethodPost, "/treasury/purchase/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreatePurchaseRequest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unconfigured currency, got %d: %s", rec.Code, rec.Body.String())
	}

	// Configure USD, then the identical request must succeed.
	treasuryStore.SetBankAccount(core.BankAccountDetails{Currency: "USD", AccountName: "Foundation", AccountNumber: "1", BankName: "Bank"})

	body2 := buildSignedEnvelopeJSON(t, pub, priv, treasuryPurchaseRequestData{
		CCTAmount: 10, Method: core.PurchaseMethodBankTransfer, BuyerEmail: "buyer@example.com",
		BuyerName: "Jane Buyer", Currency: "usd", // lowercase - handler must uppercase before lookup
	})
	req2 := httptest.NewRequest(http.MethodPost, "/treasury/purchase/create", bytes.NewReader(body2))
	rec2 := httptest.NewRecorder()
	HandleCreatePurchaseRequest(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 once USD is configured, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// TestHandleCreatePurchaseRequest_BankTransferRequiresBuyerName confirms
// the admin-matching-an-incoming-wire requirement is actually enforced,
// not just documented.
func TestHandleCreatePurchaseRequest_BankTransferRequiresBuyerName(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "foundation-bt-2"
	cctStore.Mint("foundation-bt-2", cctW(1000))
	treasuryStore.SetBankAccount(core.BankAccountDetails{Currency: "USD", AccountName: "Foundation", AccountNumber: "1", BankName: "Bank"})

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, treasuryPurchaseRequestData{
		CCTAmount: 10, Method: core.PurchaseMethodBankTransfer, BuyerEmail: "buyer@example.com",
		Currency: "USD", // no BuyerName
	})
	req := httptest.NewRequest(http.MethodPost, "/treasury/purchase/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreatePurchaseRequest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a bank transfer with no buyerName, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleTreasuryMarkPaidByBuyer_OnlyTheRealBuyerCanMarkTheirOwnPurchase
func TestHandleTreasuryMarkPaidByBuyer_OnlyTheRealBuyerCanMarkTheirOwnPurchase(t *testing.T) {
	resetApplyTestState()

	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)
	treasuryStore.CreatePurchase(&core.PurchaseRequest{
		ID: "purchase-mp-1", BuyerAddress: buyerAddr, CCTAmount: 10,
		Method: core.PurchaseMethodBankTransfer, Status: core.PurchaseAwaitingPayment, CreatedAt: 1000,
	})

	// A different wallet must be rejected.
	strangerPub, strangerPriv, _ := core.GenerateKeyPair()
	strangerBody := buildSignedEnvelopeJSON(t, strangerPub, strangerPriv, treasuryMarkPaidByBuyerData{PurchaseID: "purchase-mp-1"})
	strangerReq := httptest.NewRequest(http.MethodPost, "/treasury/purchase/mark-paid", bytes.NewReader(strangerBody))
	strangerRec := httptest.NewRecorder()
	HandleTreasuryMarkPaidByBuyer(strangerRec, strangerReq)
	if strangerRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a non-buyer trying to mark someone else's purchase paid, got %d: %s", strangerRec.Code, strangerRec.Body.String())
	}

	// The real buyer succeeds.
	buyerBody := buildSignedEnvelopeJSON(t, buyerPub, buyerPriv, treasuryMarkPaidByBuyerData{PurchaseID: "purchase-mp-1"})
	buyerReq := httptest.NewRequest(http.MethodPost, "/treasury/purchase/mark-paid", bytes.NewReader(buyerBody))
	buyerRec := httptest.NewRecorder()
	HandleTreasuryMarkPaidByBuyer(buyerRec, buyerReq)
	if buyerRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the real buyer, got %d: %s", buyerRec.Code, buyerRec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(buyerRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "pending" {
		t.Fatalf("expected status=pending, got %v", resp)
	}
}

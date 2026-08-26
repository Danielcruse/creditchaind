package core

import "testing"

// TestTreasuryStore_PurchaseInstructionsFlow exercises the full
// state machine SendInstructions/MarkBuyerPaid/ConfirmPurchase are
// built for: a purchase must move strictly forward through
// AWAITING_PAYMENT -> INSTRUCTIONS_SENT -> BUYER_MARKED_PAID ->
// CONFIRMED, and MarkBuyerPaid must refuse a token that doesn't match
// what SendInstructions actually issued.
func TestTreasuryStore_PurchaseInstructionsFlow(t *testing.T) {
	s := NewTreasuryStore()
	s.CreatePurchase(&PurchaseRequest{
		ID: "p1", BuyerAddress: "buyer1", CCTAmount: 100,
		Method: PurchaseMethodBankTransfer, BuyerEmail: "buyer@example.com",
		Status: PurchaseAwaitingPayment, CreatedAt: 1000,
	})

	// Wrong token, before instructions were even sent - must fail.
	if _, ok := s.MarkBuyerPaid("p1", "bogus-token", 1001); ok {
		t.Fatalf("expected MarkBuyerPaid to fail before instructions were sent")
	}

	p, ok := s.SendInstructions("p1", "Bank: Foo Bank, Acct 12345", "real-token", 1002)
	if !ok || p.Status != PurchaseInstructionsSent {
		t.Fatalf("expected SendInstructions to succeed and move to INSTRUCTIONS_SENT, got ok=%v status=%v", ok, p)
	}

	// Sending instructions again while already sent must be a no-op.
	if _, ok := s.SendInstructions("p1", "second attempt", "other-token", 1003); ok {
		t.Fatalf("expected a second SendInstructions call to fail (already INSTRUCTIONS_SENT)")
	}

	if _, ok := s.MarkBuyerPaid("p1", "wrong-token", 1004); ok {
		t.Fatalf("expected MarkBuyerPaid to fail with the wrong token")
	}

	p, ok = s.MarkBuyerPaid("p1", "real-token", 1005)
	if !ok || p.Status != PurchaseBuyerMarkedPaid {
		t.Fatalf("expected MarkBuyerPaid to succeed with the right token, got ok=%v status=%v", ok, p)
	}

	confirmed, ok := s.ConfirmPurchase("p1")
	if !ok || confirmed.Status != PurchaseConfirmed {
		t.Fatalf("expected ConfirmPurchase to succeed from BUYER_MARKED_PAID, got ok=%v", ok)
	}

	if _, ok := s.ConfirmPurchase("p1"); ok {
		t.Fatalf("expected confirming an already-CONFIRMED purchase to fail")
	}
}

// TestTreasuryStore_ConfirmCanSkipInstructionsDance proves the admin
// can still confirm straight from AWAITING_PAYMENT - the
// instructions/mark-paid flow is a convenience, not a mandatory gate.
func TestTreasuryStore_ConfirmCanSkipInstructionsDance(t *testing.T) {
	s := NewTreasuryStore()
	s.CreatePurchase(&PurchaseRequest{
		ID: "p2", BuyerAddress: "buyer2", CCTAmount: 50,
		Method: PurchaseMethodCryptoTransfer, Status: PurchaseAwaitingPayment, CreatedAt: 1000,
	})

	confirmed, ok := s.ConfirmPurchase("p2")
	if !ok || confirmed.Status != PurchaseConfirmed {
		t.Fatalf("expected ConfirmPurchase to succeed directly from AWAITING_PAYMENT, got ok=%v", ok)
	}
}

func TestValidPurchaseMethod(t *testing.T) {
	valid := []PurchaseMethod{
		PurchaseMethodBankTransfer, PurchaseMethodCryptoTransfer, PurchaseMethodOnlineWallet,
		PurchaseMethodCard, PurchaseMethodApplePay, PurchaseMethodGooglePay,
	}
	for _, m := range valid {
		if !ValidPurchaseMethod(m) {
			t.Errorf("expected %q to be valid", m)
		}
	}
	// PurchaseMethodCard ("CARD") is valid - but see
	// MethodNeedsPresetBankAccount/HandleCreatePurchaseRequest: it's
	// contact-info-only, never a raw card-number form. This asserts a
	// DIFFERENT string ("CARD_PAYMENT") stays invalid, guarding against
	// a hypothetical future caller inventing its own ad-hoc card-related
	// method string that bypasses ValidPurchaseMethod's own whitelist.
	if ValidPurchaseMethod("CARD_PAYMENT") {
		t.Errorf("CARD_PAYMENT must not be a valid method - only the exact whitelisted constants are")
	}
	if ValidPurchaseMethod("") {
		t.Errorf("empty method must not be valid")
	}
}

// TestTreasuryStore_BankAccountsAreKeyedByCurrencyAndFullyReplaced
// covers core.BankAccountDetails' storage: SetBankAccount is a full
// replacement per currency (not a partial merge), and ListBankAccounts
// reflects whatever's currently configured.
func TestTreasuryStore_BankAccountsAreKeyedByCurrencyAndFullyReplaced(t *testing.T) {
	s := NewTreasuryStore()

	if _, ok := s.GetBankAccount("USD"); ok {
		t.Fatalf("expected no USD account before any is configured")
	}

	s.SetBankAccount(BankAccountDetails{Currency: "USD", AccountName: "CreditChain Foundation", AccountNumber: "111", BankName: "First Bank", RoutingCode: "ABA1"})
	s.SetBankAccount(BankAccountDetails{Currency: "NGN", AccountName: "CreditChain Foundation NG", AccountNumber: "222", BankName: "GTBank", RoutingCode: "SORT2"})

	usd, ok := s.GetBankAccount("USD")
	if !ok || usd.AccountNumber != "111" {
		t.Fatalf("expected USD account with AccountNumber 111, got %+v (ok=%v)", usd, ok)
	}

	if got := s.ListBankAccounts(); len(got) != 2 {
		t.Fatalf("expected 2 configured currencies, got %d", len(got))
	}

	// Full replacement, not a merge - omitting Notes here must clear
	// any previously-set value, not silently keep it.
	s.SetBankAccount(BankAccountDetails{Currency: "USD", AccountName: "New Name", AccountNumber: "999", BankName: "Second Bank"})
	usd, ok = s.GetBankAccount("USD")
	if !ok || usd.AccountName != "New Name" || usd.AccountNumber != "999" || usd.RoutingCode != "" {
		t.Fatalf("expected a full replacement (stale RoutingCode cleared), got %+v", usd)
	}
}

// TestTreasuryStore_MarkBuyerPaidBySignedRequest covers the in-app
// "I Have Paid" path - allowed from AWAITING_PAYMENT (unlike the
// token-gated MarkBuyerPaid, which requires INSTRUCTIONS_SENT), and
// refuses a caller who isn't the purchase's own BuyerAddress.
func TestTreasuryStore_MarkBuyerPaidBySignedRequest(t *testing.T) {
	s := NewTreasuryStore()
	s.CreatePurchase(&PurchaseRequest{
		ID: "p3", BuyerAddress: "buyer3", CCTAmount: 75,
		Method: PurchaseMethodBankTransfer, Currency: "USD",
		Status: PurchaseAwaitingPayment, CreatedAt: 1000,
	})

	if _, ok := s.MarkBuyerPaidBySignedRequest("p3", "someone-else", 1001); ok {
		t.Fatalf("expected MarkBuyerPaidBySignedRequest to refuse a non-buyer caller")
	}

	p, ok := s.MarkBuyerPaidBySignedRequest("p3", "buyer3", 1002)
	if !ok || p.Status != PurchaseBuyerMarkedPaid {
		t.Fatalf("expected the real buyer to mark paid directly from AWAITING_PAYMENT, got ok=%v status=%v", ok, p)
	}

	confirmed, ok := s.ConfirmPurchase("p3")
	if !ok || confirmed.Status != PurchaseConfirmed {
		t.Fatalf("expected ConfirmPurchase to still work afterward, got ok=%v", ok)
	}
}

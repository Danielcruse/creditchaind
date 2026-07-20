package core

import "testing"

func newTestLoanRequest(id, borrower string) *LoanRequest {
	return &LoanRequest{
		ID:                 id,
		Borrower:           borrower,
		RequestedSymbol:    "YENE",
		Amount:             1000,
		CollateralSymbol:   "OETH",
		CollateralAmount:   1500,
		MaxInterestPercent: 10,
		Status:             LoanRequestOpen,
		CreatedAt:          1,
	}
}

func TestLoanRequestStore_CreateAndGet(t *testing.T) {
	s := NewLoanRequestStore()
	r := newTestLoanRequest("req-1", "borrower-1")
	s.Create(r)

	got, ok := s.Get("req-1")
	if !ok || got.Borrower != "borrower-1" {
		t.Fatalf("expected to find req-1 with Borrower borrower-1, got %+v (ok=%v)", got, ok)
	}
}

func TestLoanRequestStore_CancelOnlyByBorrowerWhileOpen(t *testing.T) {
	s := NewLoanRequestStore()
	s.Create(newTestLoanRequest("req-1", "borrower-1"))

	if _, ok := s.Cancel("req-1", "someone-else"); ok {
		t.Fatalf("expected cancel by a non-borrower to fail")
	}

	r, ok := s.Cancel("req-1", "borrower-1")
	if !ok || r.Status != LoanRequestCancelled {
		t.Fatalf("expected successful cancel to set status CANCELLED, got %+v (ok=%v)", r, ok)
	}

	if _, ok := s.Cancel("req-1", "borrower-1"); ok {
		t.Fatalf("expected cancelling an already-cancelled request to fail")
	}
}

func TestLoanRequestStore_FulfillOnlyOnceWhileOpen(t *testing.T) {
	s := NewLoanRequestStore()
	s.Create(newTestLoanRequest("req-1", "borrower-1"))

	r, ok := s.Fulfill("req-1")
	if !ok || r.Status != LoanRequestFulfilled {
		t.Fatalf("expected successful fulfill to set status FULFILLED, got %+v (ok=%v)", r, ok)
	}

	if _, ok := s.Fulfill("req-1"); ok {
		t.Fatalf("expected fulfilling an already-fulfilled request to fail (no double-fulfill)")
	}
}

func TestLoanRequestStore_ListIncludesEveryStatus(t *testing.T) {
	s := NewLoanRequestStore()
	s.Create(newTestLoanRequest("req-open", "b1"))
	cancelled := newTestLoanRequest("req-cancelled", "b2")
	s.Create(cancelled)
	s.Cancel("req-cancelled", "b2")

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected List to return both open and cancelled requests, got %d", len(list))
	}
}

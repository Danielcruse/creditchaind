package core

import "testing"

func TestShortfallReputationDelta(t *testing.T) {
	tests := []struct {
		name      string
		shortfall uint64
		owed      uint64
		want      uint64
	}{
		{"zero shortfall means zero penalty", 0, 100, 0},
		{"zero owed means zero penalty (no div by zero)", 50, 0, 0},
		{"half shortfall scales to half the ceiling", 50, 100, ReputationDeltaLoanDefault / 2},
		{"full shortfall hits the full ceiling", 100, 100, ReputationDeltaLoanDefault},
		{"a shortfall that rounds to 0 floors at the minimum", 1, 1000, ReputationDeltaLoanDefaultMinimum},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortfallReputationDelta(tt.shortfall, tt.owed); got != tt.want {
				t.Fatalf("shortfallReputationDelta(%d, %d) = %d, want %d", tt.shortfall, tt.owed, got, tt.want)
			}
		})
	}
}

// TestCreditEngine_DefaultLoan_ScalesReputationByShortfall confirms
// DefaultLoan's new (shortfall, owed) signature actually drives the
// applied reputation penalty (via shortfallReputationDelta) rather
// than the old flat ReputationDeltaLoanDefault constant.
func TestCreditEngine_DefaultLoan_ScalesReputationByShortfall(t *testing.T) {
	wallets := NewWalletStore()
	ce := &CreditEngine{Profiles: NewProfileStore(), Wallets: wallets}

	loan := &Loan{ID: "test-ce-default", Borrower: "ce-default-borrower", Status: LoanFunded}
	wallets.Get(loan.Borrower).Reputation = 50

	delta := ce.DefaultLoan(loan, 50, 100, false)

	if delta != ReputationDeltaLoanDefault/2 {
		t.Fatalf("expected delta %d (half shortfall), got %d", ReputationDeltaLoanDefault/2, delta)
	}
	if loan.Status != LoanDefault {
		t.Fatalf("expected loan status DEFAULT, got %s", loan.Status)
	}
	if got := wallets.Get(loan.Borrower).Reputation; got != 50-delta {
		t.Fatalf("expected reputation decreased by %d (from 50), got %d", delta, got)
	}
}

// TestCreditEngine_RepayLoan_OnTimeVsLate confirms the repayment
// reputation reward now actually branches on loan.DueAt vs the
// repayment timestamp, instead of always paying the on-time amount.
func TestCreditEngine_RepayLoan_OnTimeVsLate(t *testing.T) {
	tests := []struct {
		name      string
		dueAt     int64
		asOf      int64
		wantDelta uint64
		wantLate  bool
	}{
		{"no due date set - always on time", 0, 1000, ReputationDeltaLoanRepaidOnTime, false},
		{"repaid before due date - on time", 500, 400, ReputationDeltaLoanRepaidOnTime, false},
		{"repaid exactly at due date - on time", 500, 500, ReputationDeltaLoanRepaidOnTime, false},
		{"repaid after due date - late", 500, 501, ReputationDeltaLoanRepaidLate, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallets := NewWalletStore()
			ce := &CreditEngine{Profiles: NewProfileStore(), Wallets: wallets}

			loan := &Loan{ID: "test-ce-repay-" + tt.name, Borrower: "ce-repay-borrower-" + tt.name, Status: LoanFunded, DueAt: tt.dueAt}
			wallets.Get(loan.Borrower).Reputation = 50

			_, late := ce.RepayLoan(loan, tt.asOf, false)

			if late != tt.wantLate {
				t.Fatalf("late = %v, want %v", late, tt.wantLate)
			}
			if loan.Status != LoanRepaid {
				t.Fatalf("expected loan status REPAID, got %s", loan.Status)
			}
			if got := wallets.Get(loan.Borrower).Reputation; got != 50+tt.wantDelta {
				t.Fatalf("expected reputation 50+%d=%d, got %d", tt.wantDelta, 50+tt.wantDelta, got)
			}
		})
	}
}

// TestCreditEngine_DefaultLoan_ZeroShortfallSkipsReputationPenalty
// confirms a shortfall of 0 (collateral fully covered the debt) never
// touches reputation at all - see shortfallReputationDelta's doc
// comment for why this matters (ordinary market volatility that
// collateral absorbed isn't a behavioral failure).
func TestCreditEngine_DefaultLoan_ZeroShortfallSkipsReputationPenalty(t *testing.T) {
	wallets := NewWalletStore()
	ce := &CreditEngine{Profiles: NewProfileStore(), Wallets: wallets}

	loan := &Loan{ID: "test-ce-default-zero", Borrower: "ce-default-zero-borrower", Status: LoanFunded}
	wallets.Get(loan.Borrower).Reputation = 50

	delta := ce.DefaultLoan(loan, 0, 100, false)

	if delta != 0 {
		t.Fatalf("expected delta 0, got %d", delta)
	}
	if got := wallets.Get(loan.Borrower).Reputation; got != 50 {
		t.Fatalf("expected reputation untouched at 50, got %d", got)
	}
}

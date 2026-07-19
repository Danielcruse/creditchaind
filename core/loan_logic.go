package core

// CreateLoan is the self-funded borrow path only (see
// api/apply.go's applyBorrow) - always YENE, the stable settlement
// currency self-funded borrowing mints into (see the approved
// tokenomics split: CCT is utility/staking/governance, YEN-e is the
// borrow/lending currency). Loans created before this change keep
// their original USDCTEST denomination - Symbol is per-loan, nothing
// downstream assumes a global default. Peer-funded loans (via a
// LoanOffer) are constructed directly with ls.Add instead, since
// their symbol/lender vary per offer.
func (ls *LoanStore) CreateLoan(
	id string,
	borrower string,
	amount uint64,
	interest uint64,
	collateralSymbol string,
	collateralAmount uint64,
	createdAt int64,
) {

	loan := &Loan{
		ID:               id,
		Borrower:         borrower,
		Amount:           amount,
		Interest:         interest,
		Symbol:           "YENE",
		Status:           LoanFunded,
		CollateralSymbol: collateralSymbol,
		CollateralAmount: collateralAmount,
		CreatedAt:        createdAt,
	}

	ls.Add(
		loan,
	)
}

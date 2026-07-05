package core

// CreateLoan is the self-funded borrow path only (see
// api/apply.go's applyBorrow) - always USDCTEST, since that's the
// one ledger self-funded borrowing ever mints into. Peer-funded loans
// (via a LoanOffer) are constructed directly with ls.Add instead,
// since their symbol/lender vary per offer.
func (ls *LoanStore) CreateLoan(
	id string,
	borrower string,
	amount uint64,
	interest uint64,
	collateralSymbol string,
	collateralAmount uint64,
) {

	loan := &Loan{
		ID:               id,
		Borrower:         borrower,
		Amount:           amount,
		Interest:         interest,
		Symbol:           "USDCTEST",
		Status:           LoanFunded,
		CollateralSymbol: collateralSymbol,
		CollateralAmount: collateralAmount,
	}

	ls.Add(
		loan,
	)
}
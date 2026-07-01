package core
func (ls *LoanStore) CreateLoan(
	id string,
	borrower string,
	amount uint64,
	interest uint64,
) {

	loan := &Loan{
		ID:       id,
		Borrower: borrower,
		Amount:   amount,
		Interest: interest,
		Status:   LoanFunded,
	}

	ls.Add(
		loan,
	)
}
package core

func FundLoan(
	loan *Loan,
	lender string,
) {
	if loan.Status != LoanPending {
		return
	}

	loan.Lender = lender
	loan.Status = LoanFunded
}

func RepayLoan(
	loan *Loan,
) {
	if loan.Status != LoanFunded {
		return
	}

	loan.Status = LoanRepaid
}

func DefaultLoan(
	loan *Loan,
) {
	if loan.Status == LoanRepaid {
		return
	}

	loan.Status = LoanDefault
}
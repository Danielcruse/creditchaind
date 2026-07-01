package core

type CreditEngine struct {
	Profiles *ProfileStore
}

func NewCreditEngine() *CreditEngine {
	return &CreditEngine{
		Profiles: NewProfileStore(),
	}
}

func (ce *CreditEngine) FundLoan(
	loan *Loan,
	lender string,
) {
	FundLoan(
		loan,
		lender,
	)

	profile := ce.Profiles.Get(
		lender,
	)

	profile.RecordFunding(
		loan.Amount,
	)
}

func (ce *CreditEngine) RepayLoan(
	loan *Loan,
) {
	RepayLoan(
		loan,
	)

	profile := ce.Profiles.Get(
		loan.Borrower,
	)

	profile.RecordRepayment(
		loan.Amount,
	)
}

func (ce *CreditEngine) DefaultLoan(
	loan *Loan,
) {
	DefaultLoan(
		loan,
	)

	profile := ce.Profiles.Get(
		loan.Borrower,
	)

	profile.RecordDefault()
}

func (ce *CreditEngine) CalculateBorrowPower(
	reputation uint64,
	oeth uint64,
) uint64 {
	return reputation*10 + oeth*100
}
package core

type LoanStore struct {
	Loans map[string]*Loan
}

func NewLoanStore() *LoanStore {
	return &LoanStore{
		Loans: make(
			map[string]*Loan,
		),
	}
}

func (ls *LoanStore) Add(
	loan *Loan,
) {
	ls.Loans[loan.ID] = loan
}

func (ls *LoanStore) Get(
	id string,
) (*Loan, bool) {

	loan, ok := ls.Loans[id]
	return loan, ok
}
package core

type LoanStatus string

const (
	LoanPending  LoanStatus = "PENDING"
	LoanFunded   LoanStatus = "FUNDED"
	LoanRepaid   LoanStatus = "REPAID"
	LoanDefault  LoanStatus = "DEFAULT"
)

type Loan struct {
	ID        string
	Borrower  string
	Lender    string
	Amount    uint64
	Interest  uint64
	Status    LoanStatus
}
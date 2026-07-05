package core

type Liquidation struct {
	LoanID     string
	Borrower   string
	Collateral uint64
	Executed   bool
}

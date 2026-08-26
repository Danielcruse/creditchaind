package core

// RecordLoanTaken/RecordLoanRepaid/RecordLoanDefault track loan-history
// counters only. They no longer touch a Score field - reputation
// changes from loan events are applied directly to Wallet.Reputation
// via IncreaseWalletReputation/DecreaseWalletReputation (see the
// locked reputation deltas in loan_actions.go / reputation.go).
func RecordLoanTaken(p *CreditProfile) {
	p.LoansTaken++
}

func RecordLoanRepaid(p *CreditProfile) {
	p.LoansRepaid++
}

func RecordLoanDefault(p *CreditProfile) {
	p.LoansDefaulted++
}

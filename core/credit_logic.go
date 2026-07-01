package core

func RecordLoanTaken(
	p *CreditProfile,
) {
	p.LoansTaken++
	CalculateScore(p)
}

func RecordLoanRepaid(
	p *CreditProfile,
) {
	p.LoansRepaid++
	CalculateScore(p)
}

func RecordLoanDefault(
	p *CreditProfile,
) {
	p.LoansDefaulted++
	CalculateScore(p)
}
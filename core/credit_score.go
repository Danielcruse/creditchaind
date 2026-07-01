package core

type CreditProfile struct {
	Address         string
	Score           uint64
	LoansTaken      uint64
	LoansRepaid     uint64
	LoansDefaulted  uint64
	Reputation      uint64
}

func CalculateScore(
	p *CreditProfile,
) {

	score := uint64(500)

	score += p.LoansRepaid * 20
	score += p.Reputation

	if p.LoansDefaulted > 0 {
		score -= p.LoansDefaulted * 50
	}

	p.Score = score
}
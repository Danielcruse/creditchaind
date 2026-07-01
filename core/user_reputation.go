package core

type Reputation struct {
	Address     string
	Score       uint64
	LoansRepaid uint64
	LoansDefault uint64
	LoansFunded uint64
	TotalVolume uint64
}

func NewReputation(
	address string,
) Reputation {
	return Reputation{
		Address:      address,
		Score:        50,
		LoansRepaid:  0,
		LoansDefault: 0,
		LoansFunded:  0,
		TotalVolume:  0,
	}
}

func (r *Reputation) RecordRepayment(
	amount uint64,
) {
	r.LoansRepaid++
	r.TotalVolume += amount

	if r.Score < 100 {
		r.Score += 5

		if r.Score > 100 {
			r.Score = 100
		}
	}
}

func (r *Reputation) RecordFunding(
	amount uint64,
) {
	r.LoansFunded++
	r.TotalVolume += amount

	if r.Score < 100 {
		r.Score += 2

		if r.Score > 100 {
			r.Score = 100
		}
	}
}

func (r *Reputation) RecordDefault() {
	r.LoansDefault++

	if r.Score > 20 {
		r.Score -= 20
	} else {
		r.Score = 0
	}
}
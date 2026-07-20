package core

// Reputation holds loan-history counters for an address. It does NOT
// store its own trust score - Wallet.Reputation is the single
// canonical source, per the "one owner per concept" rule. CreditEngine
// applies the actual reputation deltas directly to Wallet.Reputation
// (see credit_engine.go); this struct just tracks the supporting
// history (repaid/default/funded counts, volume) for display.
type Reputation struct {
	Address      string
	LoansRepaid  uint64
	LoansDefault uint64
	LoansFunded  uint64
	TotalVolume  uint64
}

func NewReputation(address string) Reputation {
	return Reputation{
		Address: address,
	}
}

func (r *Reputation) RecordRepayment(amount uint64) {
	r.LoansRepaid++
	r.TotalVolume += amount
}

func (r *Reputation) RecordFunding(amount uint64) {
	r.LoansFunded++
	r.TotalVolume += amount
}

func (r *Reputation) RecordDefault() {
	r.LoansDefault++
}

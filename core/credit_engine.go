package core

// CreditEngine ties loan lifecycle events to the two things they
// actually affect: loan-history counters (Profiles, display-only)
// and the canonical Wallet.Reputation (the real trust score, applied
// directly here rather than through a second scoring system).
type CreditEngine struct {
	Profiles *ProfileStore
	Wallets  *WalletStore
}

func NewCreditEngine(wallets *WalletStore) *CreditEngine {
	return &CreditEngine{
		Profiles: NewProfileStore(),
		Wallets:  wallets,
	}
}

// Locked reputation deltas for loan lifecycle events (see PoSR/loan
// spec). Late-repayment (+3, within grace period) requires the loan
// due-date fields that aren't wired up yet - only on-time repayment
// (+10) and default (-30) are applied for now.
const (
	ReputationDeltaLoanRepaidOnTime = 10
	ReputationDeltaLoanRepaidLate   = 3
	ReputationDeltaLoanDefault      = 30
	// Not part of the explicitly locked spec - preserved from the
	// prior implementation as a small lender-side incentive. Flag if
	// this should be removed or changed.
	ReputationDeltaLoanFunded = 2
	// ReputationDeltaLoanDefaultLenderGain is the flip side of
	// ReputationDeltaLoanDefault - the peer-funded loan marketplace's
	// core economics are that the lender is always made whole, never
	// just left even: a defaulting borrower's reputation loss is framed
	// as a transfer, not just a penalty, so the lender who bore none of
	// the risk still gains from the outcome (mirrors gaining their
	// collateral too - see api/apply.go's liquidateLoan). Kept as the
	// historical ceiling shortfallReputationDelta scales down from, not
	// a flat amount actually applied anymore - see that function.
	ReputationDeltaLoanDefaultLenderGain = ReputationDeltaLoanDefault
	// ReputationDeltaLoanDefaultMinimum is the smallest reputation
	// penalty a nonzero shortfall can ever produce (see
	// shortfallReputationDelta) - keeps a small shortfall from silently
	// rounding down to a 0-point penalty, which would look identical to
	// "the lender was made whole."
	ReputationDeltaLoanDefaultMinimum = 1
)

// shortfallReputationDelta scales the loan-default reputation penalty
// to how much of the debt liquidation actually failed to cover, per
// the approved tokenomics spec: reputation impact is tied to what
// liquidation failed to cover, not to the act of being liquidated at
// all - a shortfall of 0 (collateral fully protected the lender) means
// 0 penalty, since that's ordinary market volatility the borrower's
// collateral absorbed, not a behavioral failure. A full shortfall (no
// collateral recovery at all, e.g. CCT collateral or no live price)
// means the full ReputationDeltaLoanDefault penalty - identical to
// this system's original flat behavior before shortfall-awareness
// existed. Anything in between scales linearly, floored at
// ReputationDeltaLoanDefaultMinimum once shortfall > 0.
func shortfallReputationDelta(shortfall, owed uint64) uint64 {
	if shortfall == 0 || owed == 0 {
		return 0
	}

	delta := ReputationDeltaLoanDefault * shortfall / owed
	if delta > ReputationDeltaLoanDefault {
		delta = ReputationDeltaLoanDefault
	}
	if delta < ReputationDeltaLoanDefaultMinimum {
		delta = ReputationDeltaLoanDefaultMinimum
	}
	return delta
}

// FundLoan/RepayLoan return the REPT reward earned (0 if none) - see
// IncreaseWalletReputation. The caller (api/apply.go) mints it; core
// only computes whether a new tier was genuinely reached.

func (ce *CreditEngine) FundLoan(loan *Loan, lender string) uint64 {
	FundLoan(loan, lender)

	profile := ce.Profiles.Get(lender)
	profile.RecordFunding(loan.Amount)

	return IncreaseWalletReputation(ce.Wallets.Get(lender), ReputationDeltaLoanFunded)
}

func (ce *CreditEngine) RepayLoan(loan *Loan) uint64 {
	RepayLoan(loan)

	profile := ce.Profiles.Get(loan.Borrower)
	profile.RecordRepayment(loan.Amount)

	// TODO: once Loan.DueAt/GracePeriod are wired in, branch here on
	// on-time vs late repayment (+10 vs +3). Treated as on-time until then.
	return IncreaseWalletReputation(ce.Wallets.Get(loan.Borrower), ReputationDeltaLoanRepaidOnTime)
}

// DefaultLoan flips the loan to DEFAULT and applies a reputation
// penalty scaled to shortfall/owed (see shortfallReputationDelta) -
// shortfall is what liquidation failed to recover, owed is the full
// repayAmountFor(loan) the lender was due. Returns the delta actually
// applied (0 if shortfall was 0) so the caller (api/apply.go's
// processLoanDefaults) can mirror the identical magnitude into the
// lender's reputation gain and skip recording a zero-magnitude event.
func (ce *CreditEngine) DefaultLoan(loan *Loan, shortfall, owed uint64) uint64 {
	DefaultLoan(loan)

	profile := ce.Profiles.Get(loan.Borrower)
	profile.RecordDefault()

	delta := shortfallReputationDelta(shortfall, owed)
	if delta > 0 {
		DecreaseWalletReputation(ce.Wallets.Get(loan.Borrower), delta)
	}
	return delta
}

func (ce *CreditEngine) CalculateBorrowPower(reputation uint64, oeth uint64) uint64 {
	return reputation*10 + oeth*100
}

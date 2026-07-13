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
	// collateral too - see api/apply.go's forfeitLoanCollateralToLender).
	ReputationDeltaLoanDefaultLenderGain = ReputationDeltaLoanDefault
)

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

func (ce *CreditEngine) DefaultLoan(loan *Loan) {
	DefaultLoan(loan)

	profile := ce.Profiles.Get(loan.Borrower)
	profile.RecordDefault()

	DecreaseWalletReputation(ce.Wallets.Get(loan.Borrower), ReputationDeltaLoanDefault)
}

func (ce *CreditEngine) CalculateBorrowPower(reputation uint64, oeth uint64) uint64 {
	return reputation*10 + oeth*100
}

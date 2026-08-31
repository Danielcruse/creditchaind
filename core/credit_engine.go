package core

// CreditEngine ties loan lifecycle events to the two things they
// actually affect: loan-history counters (Profiles, display-only) and
// Wallet's trust score, applied directly here rather than through a
// second scoring system. Pre-TrustDimensionsCutoverUnixSeconds, that
// means the flat Wallet.Reputation directly; post-cutover, it means a
// specific Wallet.Trust dimension (see trust.go), with Reputation kept
// as a live-synced mirror - see reputation.go's IncreaseWalletTrust/
// DecreaseWalletTrust doc comments for the full split.
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
// spec).
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

// dimensioned in each method below is the caller's own
// TrustDimensionsCutoverUnixSeconds check (api/apply.go) - core has no
// business importing api's cutover schedule (matches this codebase's
// existing convention: every *CutoverUnixSeconds constant lives in
// api/apply.go, core functions that need cutover-gated behavior take
// the already-decided bool from their caller instead), so the decision
// is made once by whichever api/apply.go call site invokes these and
// threaded straight through.
func (ce *CreditEngine) FundLoan(loan *Loan, lender string, dimensioned bool) uint64 {
	FundLoan(loan, lender)

	profile := ce.Profiles.Get(lender)
	profile.RecordFunding(loan.Amount)

	// Contribution, not Repayment - a lender isn't demonstrating their
	// own repayment reliability, they're providing liquidity, which is
	// an ecosystem contribution (see trust.go's mapping doc comment).
	if dimensioned {
		return IncreaseWalletTrust(ce.Wallets.Get(lender), DimensionContribution, ReputationDeltaLoanFunded)
	}
	return IncreaseWalletReputation(ce.Wallets.Get(lender), ReputationDeltaLoanFunded)
}

// loanRepaymentDelta decides whether a peer-funded loan's repayment
// counts as on-time (+10) or late (+3), per the locked PoSR spec's
// two-tier repayment reward. "Late" means past loan.DueAt - which, in
// this system's staged Default Protection sequence (see loan_risk.go),
// covers a repayment made during Warning/MarginCall/GracePeriod too:
// loan.Status stays LoanFunded (so RepayLoan is still reachable at
// all) all the way through those stages, so any repayment that gets
// here past DueAt is by definition "within grace period," never a
// default - a default only happens via DefaultLoan, once the grace
// period itself has expired unpaid (see api/apply.go's
// advanceLoanLifecycle/processFinalRedemptions). A DueAt of 0 (no due
// date - see Loan.DueAt's own doc comment) can never be late.
func loanRepaymentDelta(loan *Loan, asOf int64) (delta uint64, late bool) {
	if loan.DueAt > 0 && asOf > loan.DueAt {
		return ReputationDeltaLoanRepaidLate, true
	}
	return ReputationDeltaLoanRepaidOnTime, false
}

// RepayLoan applies the loan-repaid reputation reward, scaled by
// loanRepaymentDelta above - asOf is the repayment transaction's own
// timestamp, never wall-clock (matches this codebase's existing
// block-timestamp-driven determinism). Returns the REPT tier-crossing
// reward (0 if none, see IncreaseWalletTrust/IncreaseWalletReputation)
// and whether this repayment was late, so the caller can log the
// correct magnitude/reason to reputationEventStore.
func (ce *CreditEngine) RepayLoan(loan *Loan, asOf int64, dimensioned bool) (reward uint64, late bool) {
	delta, late := loanRepaymentDelta(loan, asOf)

	RepayLoan(loan)

	profile := ce.Profiles.Get(loan.Borrower)
	profile.RecordRepayment(loan.Amount)

	if dimensioned {
		reward = IncreaseWalletTrust(ce.Wallets.Get(loan.Borrower), DimensionRepayment, delta)
	} else {
		reward = IncreaseWalletReputation(ce.Wallets.Get(loan.Borrower), delta)
	}
	return reward, late
}

// DefaultLoan flips the loan to DEFAULT and applies a reputation
// penalty scaled to shortfall/owed (see shortfallReputationDelta) -
// shortfall is what liquidation failed to recover, owed is the full
// repayAmountFor(loan) the lender was due. Returns the delta actually
// applied (0 if shortfall was 0) so the caller (api/apply.go's
// processLoanDefaults) can mirror the identical magnitude into the
// lender's reputation gain and skip recording a zero-magnitude event.
func (ce *CreditEngine) DefaultLoan(loan *Loan, shortfall, owed uint64, dimensioned bool) uint64 {
	DefaultLoan(loan)

	profile := ce.Profiles.Get(loan.Borrower)
	profile.RecordDefault()

	delta := shortfallReputationDelta(shortfall, owed)
	if delta > 0 {
		if dimensioned {
			DecreaseWalletTrust(ce.Wallets.Get(loan.Borrower), DimensionRepayment, delta)
		} else {
			DecreaseWalletReputation(ce.Wallets.Get(loan.Borrower), delta)
		}
	}
	return delta
}

func (ce *CreditEngine) CalculateBorrowPower(reputation uint64, oeth uint64) uint64 {
	return reputation*10 + oeth*100
}

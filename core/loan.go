package core

type LoanStatus string

const (
	LoanPending LoanStatus = "PENDING"
	LoanFunded  LoanStatus = "FUNDED"
	LoanRepaid  LoanStatus = "REPAID"
	LoanDefault LoanStatus = "DEFAULT"
)

type Loan struct {
	ID       string
	Borrower string
	Lender   string
	// LenderPublicKey is the lender's authenticated public key,
	// captured at draw time from the LoanOffer (empty for a
	// self-funded loan, which has no real lender) - needed to
	// re-derive the lender's ETHTEST/USDCTEST sub-address on
	// repayment, the same reason core.P2POffer/core.LoanOffer each
	// carry their own creator/lender public key.
	LenderPublicKey string
	// Symbol is which token this loan is denominated in - CCT,
	// ETHTEST, USDCTEST, YENE, REPT, or a developer-registered token.
	// Self-funded loans (see api/apply.go's applyBorrow) default to
	// YENE, the stable settlement currency (older loans drawn before
	// this default changed keep whatever they were created with, e.g.
	// USDCTEST); peer-funded loans (via a LoanOffer) use whatever
	// symbol the lender's offer specified.
	Symbol   string
	Amount   uint64
	Interest uint64
	Status   LoanStatus

	// CollateralSymbol/CollateralAmount record exactly which locked
	// collateral backs THIS loan - "CCT" (staked CCT) or "OETH",
	// reserved out of the borrower's CollateralStore pool at borrow
	// time (see api/apply.go's reserveCollateralForLoan) and returned
	// to that same staked/oETH balance on repayment (see
	// releaseLoanCollateral) - never to liquid CCT, and never by just
	// assuming the collateral amount equals the loan amount the way
	// this worked before these fields existed. Zero/empty for a loan
	// that predates this (nothing to release).
	CollateralSymbol string
	CollateralAmount uint64

	// DueAt is when this loan must be repaid by - fundedAt +
	// the lender's offer duration (see core.LoanOffer.DurationSeconds),
	// computed once at draw time so it never moves even if the offer
	// itself changes later. Zero means no due date is enforced (a
	// self-funded loan via plain /borrow, which has no real lender to
	// protect, or a loan drawn against a pre-this-feature offer with
	// no duration set) - see api/apply.go's processLoanDefaults, which
	// only ever sweeps loans with DueAt > 0.
	DueAt int64

	// CreatedAt/RepaidAt are the transaction timestamps of this loan's
	// funding and (if applicable) repayment - added for age/duration
	// display and future repayment-time analytics. Zero on either
	// field means "predates this field" (loans created before this
	// change) - never compute a duration/age from a zero timestamp,
	// show "unknown" instead.
	CreatedAt int64
	RepaidAt  int64
}

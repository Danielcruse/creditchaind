package core

// TrustDimensions splits the single flat Reputation score (see
// reputation.go) into independently-tracked trust signals, so a
// weakness in one area (e.g. a marketplace dispute) doesn't
// automatically drag down standing in an unrelated area (e.g. loan
// eligibility) - the actual problem the flat single-number design had.
// Every dimension shares the exact same 1-100 scale and floor/cap
// semantics as the legacy Reputation field (MinReputation/MaxReputation
// below) - deliberately not a second scale to learn.
//
// Named fields, not a map[string]uint64: compile-time-checked field
// names, no risk of a typo'd key silently no-opping a mutation, and it
// matches Wallet's own all-explicit-fields style. TrustDimension (the
// enum below) plus the get/set helpers are what let the mutation API
// in reputation.go stay dimension-parameterized without falling back
// to string keys or reflection.
type TrustDimensions struct {
	RepaymentTrust    uint64
	TradingTrust      uint64
	GovernanceTrust   uint64
	ContributionTrust uint64
	VerificationTrust uint64
	DisputeTrust      uint64
}

// TrustDimension identifies one of TrustDimensions' six fields for the
// dimension-parameterized mutation API (IncreaseWalletTrust/
// DecreaseWalletTrust, reputation.go).
type TrustDimension int

const (
	DimensionRepayment TrustDimension = iota
	DimensionTrading
	DimensionGovernance
	DimensionContribution
	DimensionVerification
	DimensionDispute
)

func (d TrustDimension) String() string {
	switch d {
	case DimensionRepayment:
		return "Repayment"
	case DimensionTrading:
		return "Trading"
	case DimensionGovernance:
		return "Governance"
	case DimensionContribution:
		return "Contribution"
	case DimensionVerification:
		return "Verification"
	case DimensionDispute:
		return "Dispute"
	default:
		return "Unknown"
	}
}

func (t TrustDimensions) get(d TrustDimension) uint64 {
	switch d {
	case DimensionRepayment:
		return t.RepaymentTrust
	case DimensionTrading:
		return t.TradingTrust
	case DimensionGovernance:
		return t.GovernanceTrust
	case DimensionContribution:
		return t.ContributionTrust
	case DimensionVerification:
		return t.VerificationTrust
	case DimensionDispute:
		return t.DisputeTrust
	default:
		return 0
	}
}

func (t *TrustDimensions) set(d TrustDimension, v uint64) {
	switch d {
	case DimensionRepayment:
		t.RepaymentTrust = v
	case DimensionTrading:
		t.TradingTrust = v
	case DimensionGovernance:
		t.GovernanceTrust = v
	case DimensionContribution:
		t.ContributionTrust = v
	case DimensionVerification:
		t.VerificationTrust = v
	case DimensionDispute:
		t.DisputeTrust = v
	}
}

// isZero reports whether every dimension is still at its Go zero
// value - the "not yet seeded" sentinel used by seedTrustIfNeeded.
// Safe because the floor (MinReputation=1) means a genuinely-seeded
// wallet can never have all six fields sit at exactly 0 - only a
// wallet that predates TrustDimensions existing does.
func (t TrustDimensions) isZero() bool {
	return t == TrustDimensions{}
}

// TrustWeights are the basis-point weights ComputeTSI blends the six
// dimensions with. Deliberately NOT required to sum to 10000 - ComputeTSI
// always normalizes by the live sum, so a future pass can make these
// governance-adjustable one key at a time (matching this codebase's
// existing single-key governanceParamsStore pattern, api/governance_params.go)
// without ComputeTSI itself ever needing to change.
type TrustWeights struct {
	RepaymentBps    uint64
	TradingBps      uint64
	GovernanceBps   uint64
	ContributionBps uint64
	VerificationBps uint64
	DisputeBps      uint64
}

// DefaultTrustWeights is the starting split confirmed for CreditChain's
// TSI: Repayment 30% / Trading 22% / Governance 18% / Contribution 14% /
// Verification 8% / Dispute 8%. Dispute Trust deliberately gets its own
// slice rather than being folded into Trading - dispute events (fraud
// findings, moderator abuse) are large-magnitude (30pts, the same class
// as the moderator-abuse penalty) and specifically encode dispute
// conduct; folding them into Trading would let one fraud finding tank
// routine marketplace standing too, undercutting the whole point of
// splitting dimensions apart in the first place.
var DefaultTrustWeights = TrustWeights{
	RepaymentBps:    3000,
	TradingBps:      2200,
	GovernanceBps:   1800,
	ContributionBps: 1400,
	VerificationBps: 800,
	DisputeBps:      800,
}

// ComputeTSI is the overall Trust Score Index - the governance-tunable
// weighted blend of the six dimensions. Always divides by the LIVE sum
// of the given weights rather than assuming they already total exactly
// 10000bps: this is what lets a future pass change one dimension's
// weight at a time (the only kind of write governanceParamsStore's
// existing single-key mechanism supports, see api/governance_params.go)
// without this function ever needing to re-validate or reject a
// mid-transition, momentarily-non-normalized weight set. Falls back to
// DefaultTrustWeights if the given weights sum to zero (defensive -
// should never happen in practice, but a divide-by-zero here would be
// far worse than silently using the default split for one read).
func ComputeTSI(t TrustDimensions, w TrustWeights) uint64 {
	sum := w.RepaymentBps + w.TradingBps + w.GovernanceBps + w.ContributionBps + w.VerificationBps + w.DisputeBps
	if sum == 0 {
		w = DefaultTrustWeights
		sum = w.RepaymentBps + w.TradingBps + w.GovernanceBps + w.ContributionBps + w.VerificationBps + w.DisputeBps
	}

	weighted := t.RepaymentTrust*w.RepaymentBps +
		t.TradingTrust*w.TradingBps +
		t.GovernanceTrust*w.GovernanceBps +
		t.ContributionTrust*w.ContributionBps +
		t.VerificationTrust*w.VerificationBps +
		t.DisputeTrust*w.DisputeBps

	return clampReputation(weighted / sum)
}

// seedTrustIfNeeded lazily backfills a pre-TrustDimensions wallet's six
// dimensions from its existing flat Reputation value, the first time
// any post-cutover transaction touches it (see reputation.go's
// IncreaseWalletTrust/DecreaseWalletTrust, and
// TrustDimensionsCutoverUnixSeconds in api/apply.go). Deterministic
// across nodes: the seed source (w.Reputation at the moment of first
// post-cutover touch) is itself already consensus-agreed state, so two
// nodes touching the same wallet via different event types on
// different blocks still converge on an identical seed. A no-op for
// any wallet that already has real per-dimension history, or that was
// created via NewWallet() (which sets all six explicitly, see
// wallet.go) and so never has a zero Trust value to seed from at all.
// TrustDimensionValue is the read-only counterpart to
// IncreaseWalletTrust/DecreaseWalletTrust - returns the effective value
// of one dimension for gates/weight-consumers that need to read (not
// mutate) trust, e.g. BorrowLimit/InterestRate keying off Repayment
// Trust specifically. Deliberately does NOT call seedTrustIfNeeded
// (which mutates the wallet) - a GET request or a read-only weight
// calculation must never have the side effect of writing to a wallet
// just because someone looked at their borrowing power. Falls back to
// the flat Reputation for a wallet whose Trust hasn't been seeded yet
// (isZero) - the exact same value seedTrustIfNeeded would eventually
// seed every dimension to anyway, so this is never wrong, just
// possibly stale-until-first-mutation for a not-yet-touched wallet.
func TrustDimensionValue(w *Wallet, d TrustDimension) uint64 {
	if w.Trust.isZero() {
		return w.Reputation
	}
	return w.Trust.get(d)
}

func seedTrustIfNeeded(w *Wallet) {
	if !w.Trust.isZero() {
		return
	}
	seed := clampReputation(w.Reputation)
	w.Trust = TrustDimensions{
		RepaymentTrust:    seed,
		TradingTrust:      seed,
		GovernanceTrust:   seed,
		ContributionTrust: seed,
		VerificationTrust: seed,
		DisputeTrust:      seed,
	}
}

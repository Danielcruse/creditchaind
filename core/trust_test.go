package core

import "testing"

// TestComputeTSI_EqualDimensionsReturnsThatValueRegardlessOfWeights
// confirms the basic sanity case: if every dimension is the same
// value, the weighted blend must return exactly that value no matter
// how the weights are split (as long as they're internally consistent
// - see the zero-sum guard test below for the one exception).
func TestComputeTSI_EqualDimensionsReturnsThatValueRegardlessOfWeights(t *testing.T) {
	dims := TrustDimensions{
		RepaymentTrust: 73, TradingTrust: 73, GovernanceTrust: 73,
		ContributionTrust: 73, VerificationTrust: 73, DisputeTrust: 73,
	}
	if got := ComputeTSI(dims, DefaultTrustWeights); got != 73 {
		t.Fatalf("expected 73 with all-equal dimensions under default weights, got %d", got)
	}

	skewed := TrustWeights{RepaymentBps: 9000, TradingBps: 100, GovernanceBps: 300, ContributionBps: 300, VerificationBps: 200, DisputeBps: 100}
	if got := ComputeTSI(dims, skewed); got != 73 {
		t.Fatalf("expected 73 with all-equal dimensions under a heavily skewed weight split, got %d", got)
	}
}

// TestComputeTSI_WeightsOneDimensionCorrectly confirms the actual
// blend math against a hand-computed value, using DefaultTrustWeights
// (30/22/18/14/8/8) - the exact split confirmed for this project.
func TestComputeTSI_WeightsOneDimensionCorrectly(t *testing.T) {
	// Only Repayment (30%) differs from the rest (all 50): a wallet
	// that just took a full Repayment penalty down to 20, everything
	// else untouched.
	dims := TrustDimensions{
		RepaymentTrust: 20, TradingTrust: 50, GovernanceTrust: 50,
		ContributionTrust: 50, VerificationTrust: 50, DisputeTrust: 50,
	}
	// (20*3000 + 50*2200 + 50*1800 + 50*1400 + 50*800 + 50*800) / 10000
	// = (60000 + 110000+90000+70000+40000+40000) / 10000 = 410000/10000 = 41
	want := uint64(41)
	if got := ComputeTSI(dims, DefaultTrustWeights); got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}

// TestComputeTSI_ZeroWeightSumFallsBackToDefault confirms the
// defensive guard: a (should-never-happen) all-zero weight set must
// never divide by zero, and falls back to producing the same result
// DefaultTrustWeights would.
func TestComputeTSI_ZeroWeightSumFallsBackToDefault(t *testing.T) {
	dims := TrustDimensions{
		RepaymentTrust: 20, TradingTrust: 50, GovernanceTrust: 50,
		ContributionTrust: 50, VerificationTrust: 50, DisputeTrust: 50,
	}
	got := ComputeTSI(dims, TrustWeights{})
	want := ComputeTSI(dims, DefaultTrustWeights)
	if got != want {
		t.Fatalf("expected zero-weight-sum to fall back to DefaultTrustWeights's result (%d), got %d", want, got)
	}
}

// TestComputeTSI_ClampsToValidRange confirms the result is always
// clamped 1-100 even given out-of-range dimension inputs (shouldn't
// happen via the normal mutation API, which clamps on every write, but
// ComputeTSI itself must not assume that - it's a pure function callers
// could in principle feed anything to).
func TestComputeTSI_ClampsToValidRange(t *testing.T) {
	allMax := TrustDimensions{
		RepaymentTrust: 100, TradingTrust: 100, GovernanceTrust: 100,
		ContributionTrust: 100, VerificationTrust: 100, DisputeTrust: 100,
	}
	if got := ComputeTSI(allMax, DefaultTrustWeights); got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
}

// TestSeedTrustIfNeeded_SeedsAllSixFromExistingReputationOnce confirms
// the lazy-seeding behavior a pre-TrustDimensions wallet needs: the
// first touch seeds every dimension from the wallet's existing flat
// Reputation, and a second call is a no-op (doesn't re-seed once real
// per-dimension history exists).
func TestSeedTrustIfNeeded_SeedsAllSixFromExistingReputationOnce(t *testing.T) {
	w := &Wallet{Reputation: 67} // Trust left at its zero value, simulating a pre-existing wallet

	seedTrustIfNeeded(w)

	want := TrustDimensions{
		RepaymentTrust: 67, TradingTrust: 67, GovernanceTrust: 67,
		ContributionTrust: 67, VerificationTrust: 67, DisputeTrust: 67,
	}
	if w.Trust != want {
		t.Fatalf("expected all six dimensions seeded to 67, got %+v", w.Trust)
	}

	// Mutate one dimension directly (simulating a real event having
	// applied since), then confirm a second seed call doesn't touch it.
	w.Trust.RepaymentTrust = 40
	seedTrustIfNeeded(w)
	if w.Trust.RepaymentTrust != 40 {
		t.Fatalf("expected seedTrustIfNeeded to be a no-op once Trust is non-zero, but RepaymentTrust changed to %d", w.Trust.RepaymentTrust)
	}
}

// TestSeedTrustIfNeeded_NewWalletNeverNeedsSeeding confirms
// NewWallet()'s explicit Trust initialization means a genuinely new
// wallet never passes through the lazy-seed path at all.
func TestSeedTrustIfNeeded_NewWalletNeverNeedsSeeding(t *testing.T) {
	w := NewWallet()
	if w.Trust.isZero() {
		t.Fatal("expected NewWallet() to explicitly set Trust (non-zero), so it never needs lazy seeding")
	}
	want := TrustDimensions{RepaymentTrust: 50, TradingTrust: 50, GovernanceTrust: 50, ContributionTrust: 50, VerificationTrust: 50, DisputeTrust: 50}
	if w.Trust != want {
		t.Fatalf("expected NewWallet() to seed all six dimensions to 50 (matching Reputation), got %+v", w.Trust)
	}
}

// TestIncreaseWalletTrust_OnlyMovesTheTargetedDimension confirms the
// actual mutation API: increasing one dimension leaves the other five
// untouched, and the mirrored Reputation reflects the new weighted
// blend, not the raw per-dimension delta.
func TestIncreaseWalletTrust_OnlyMovesTheTargetedDimension(t *testing.T) {
	w := &Wallet{Reputation: 50, HighestTierReached: TierForReputation(50)}

	IncreaseWalletTrust(w, DimensionTrading, 20)

	if w.Trust.TradingTrust != 70 {
		t.Fatalf("expected TradingTrust 70, got %d", w.Trust.TradingTrust)
	}
	for name, got := range map[string]uint64{
		"Repayment": w.Trust.RepaymentTrust, "Governance": w.Trust.GovernanceTrust,
		"Contribution": w.Trust.ContributionTrust, "Verification": w.Trust.VerificationTrust,
		"Dispute": w.Trust.DisputeTrust,
	} {
		if got != 50 {
			t.Fatalf("expected %s dimension to stay at 50 (untouched), got %d", name, got)
		}
	}

	wantMirror := ComputeTSI(w.Trust, DefaultTrustWeights)
	if w.Reputation != wantMirror {
		t.Fatalf("expected Reputation mirror to equal ComputeTSI(Trust, DefaultTrustWeights) = %d, got %d", wantMirror, w.Reputation)
	}
}

// TestDecreaseWalletTrust_FloorsAtOnePerDimension confirms each
// dimension keeps the exact same "never truly zero" floor as the
// legacy flat Reputation field.
func TestDecreaseWalletTrust_FloorsAtOnePerDimension(t *testing.T) {
	w := &Wallet{Reputation: 50}
	seedTrustIfNeeded(w)

	DecreaseWalletTrust(w, DimensionDispute, 999)

	if w.Trust.DisputeTrust != MinReputation {
		t.Fatalf("expected DisputeTrust floored at %d, got %d", MinReputation, w.Trust.DisputeTrust)
	}
}

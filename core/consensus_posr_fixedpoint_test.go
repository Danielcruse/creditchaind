package core

import "testing"

// TestValidatorWeight_MaxInputsReturnExactlyFullPrecision confirms a
// validator at max stake/reputation/uptime with no slashes returns
// exactly posrPrecision - the fixed-point scheme's "100%" reference
// point, and a case that would be lossy under naive integer division
// if the weight fractions didn't sum to exactly posrPrecision.
func TestValidatorWeight_MaxInputsReturnExactlyFullPrecision(t *testing.T) {
	v := NewValidator(StakeCap)
	v.Wallet.Reputation = MaxReputation
	v.Uptime = 1.0

	if got := validatorWeight(v); got != posrPrecision {
		t.Fatalf("expected max-input weight to equal posrPrecision (%d), got %d", uint64(posrPrecision), got)
	}
}

// TestValidatorWeight_ZeroInputsReturnZero confirms a validator with
// no stake, no reputation, and no uptime gets a zero weight - such a
// validator must never be selected (selectFrom explicitly skips
// zero-weight entries).
func TestValidatorWeight_ZeroInputsReturnZero(t *testing.T) {
	v := NewValidator(0)
	v.Wallet.Reputation = 0
	v.Uptime = 0

	if got := validatorWeight(v); got != 0 {
		t.Fatalf("expected zero-input weight to be 0, got %d", got)
	}
}

// TestValidatorWeight_StakeIsCappedNotOverweighted confirms staking
// far beyond StakeCap doesn't grant extra weight over staking exactly
// StakeCap - the whole point of the cap (see this file's own doc
// comment: "no single whale can dominate selection purely through
// wealth").
func TestValidatorWeight_StakeIsCappedNotOverweighted(t *testing.T) {
	atCap := NewValidator(StakeCap)
	atCap.Wallet.Reputation = 50
	atCap.Uptime = 0.95

	farBeyondCap := NewValidator(StakeCap * 100)
	farBeyondCap.Wallet.Reputation = 50
	farBeyondCap.Uptime = 0.95

	wAtCap := validatorWeight(atCap)
	wBeyondCap := validatorWeight(farBeyondCap)
	if wAtCap != wBeyondCap {
		t.Fatalf("expected stake beyond StakeCap to be capped to the same weight as stake at cap, got %d vs %d", wAtCap, wBeyondCap)
	}
}

// TestValidatorWeight_SlashDecayCompoundsAtNinetyPercentPerStrike
// confirms the integer repeated-multiplication decay matches the
// documented 0.9-per-strike compounding decay math.Pow used to
// compute, within the rounding error inherent to truncating integer
// division at each step.
func TestValidatorWeight_SlashDecayCompoundsAtNinetyPercentPerStrike(t *testing.T) {
	base := NewValidator(StakeCap)
	base.Wallet.Reputation = MaxReputation
	base.Uptime = 1.0

	oneStrike := *base
	oneStrike.SlashCount = 1
	wOneStrike := validatorWeight(&oneStrike)

	// posrPrecision * 9/10, matching validatorWeight's own integer
	// truncation order (decay = decay * 9 / 10 once).
	wantOneStrike := uint64(posrPrecision) * 9 / 10
	if wOneStrike != wantOneStrike {
		t.Fatalf("expected one-strike weight %d (posrPrecision*9/10), got %d", wantOneStrike, wOneStrike)
	}

	twoStrikes := *base
	twoStrikes.SlashCount = 2
	wTwoStrikes := validatorWeight(&twoStrikes)
	if wTwoStrikes >= wOneStrike {
		t.Fatalf("expected weight to keep decreasing with more strikes: one=%d two=%d", wOneStrike, wTwoStrikes)
	}

	// Compounding, not linear: two strikes should land near
	// posrPrecision*0.81, clearly above a hypothetical linear 20% cut
	// (posrPrecision*0.8) minus a wide truncation-error margin.
	linearFloor := uint64(posrPrecision) * 8 / 10
	if wTwoStrikes <= linearFloor {
		t.Fatalf("expected compounding decay (~81%%) to stay above a linear 20%%-cut floor (%d), got %d", linearFloor, wTwoStrikes)
	}
}

// TestValidatorWeight_ManyStrikesDecaysToZeroWithoutHanging exercises
// the early-exit-at-zero optimization in validatorWeight's decay loop
// with a SlashCount far beyond what core.PermanentBanSlashCountThreshold
// would ever let reach real consensus code, confirming it terminates
// promptly (not looping SlashCount times) and settles at weight 0.
func TestValidatorWeight_ManyStrikesDecaysToZeroWithoutHanging(t *testing.T) {
	v := NewValidator(StakeCap)
	v.Wallet.Reputation = MaxReputation
	v.Uptime = 1.0
	v.SlashCount = 1_000_000_000

	if got := validatorWeight(v); got != 0 {
		t.Fatalf("expected an enormous SlashCount to decay weight to 0, got %d", got)
	}
}

// TestValidatorWeight_IsFullyDeterministicAcrossRepeatedCalls confirms
// repeated calls with identical inputs always produce the identical
// output - the entire point of moving off math.Pow. Integer
// arithmetic makes this trivially true, but the test documents the
// property this whole fix exists to guarantee.
func TestValidatorWeight_IsFullyDeterministicAcrossRepeatedCalls(t *testing.T) {
	v := NewValidator(43_210)
	v.Wallet.Reputation = 67
	v.Uptime = 0.9321
	v.SlashCount = 3

	first := validatorWeight(v)
	for i := 0; i < 1000; i++ {
		if got := validatorWeight(v); got != first {
			t.Fatalf("expected validatorWeight to be fully deterministic, got %d then %d on iteration %d", first, got, i)
		}
	}
}

// TestSelectFrom_SkippedZeroWeightValidatorDoesNotMisalignSelection
// is a regression test for a latent indexing bug the float64-to-
// fixed-point rewrite also fixed: selectFrom used to build a separate
// weights slice that omitted zero-weight validators, then index into
// the ORIGINAL pool using the shortened slice's index - silently
// selecting the wrong validator whenever a zero-weight validator was
// anywhere but the end of the pool. Puts a zero-weight validator in
// the middle and confirms every returned validator is one of the two
// genuinely-eligible ones, never the zero-weight validator, across
// many seeds.
func TestSelectFrom_SkippedZeroWeightValidatorDoesNotMisalignSelection(t *testing.T) {
	first := NewValidator(StakeCap)
	first.Wallet.Reputation = MaxReputation
	first.Uptime = 1.0

	zeroWeight := NewValidator(0)
	zeroWeight.Wallet.Reputation = 0
	zeroWeight.Uptime = 0

	last := NewValidator(StakeCap)
	last.Wallet.Reputation = MaxReputation
	last.Uptime = 1.0

	pool := []*Validator{first, zeroWeight, last}

	seenFirst, seenLast := false, false
	for _, seed := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		got := selectFrom(pool, seed)
		if got == zeroWeight {
			t.Fatalf("expected the zero-weight validator to never be selected (seed %q)", seed)
		}
		if got != first && got != last {
			t.Fatalf("expected only the two genuinely-eligible validators to be selected, got an unrecognized validator (seed %q)", seed)
		}
		if got == first {
			seenFirst = true
		}
		if got == last {
			seenLast = true
		}
	}
	if !seenFirst || !seenLast {
		t.Fatalf("expected both eligible validators to be reachable across seeds (first=%v last=%v)", seenFirst, seenLast)
	}
}

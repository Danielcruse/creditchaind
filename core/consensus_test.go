package core

import "testing"

// TestSelectValidator_BootstrapTierBreaksFullDeadlock reproduces the
// exact scenario that froze node1's block production after the CCT-
// stake eligibility fix shipped: every registered validator has
// Stake below MinValidatorStake (so neither the strict nor the
// isEligibleRelaxed pool has anyone), but at least one is still
// Active. SelectValidator must still return a non-nil validator via
// isEligibleBootstrap, or block production stops forever.
func TestSelectValidator_BootstrapTierBreaksFullDeadlock(t *testing.T) {
	v1 := NewValidator(0)
	v1.Wallet.Reputation = 100
	v1.SlashCount = 127
	v1.Uptime = 0.999

	v2 := NewValidator(0)
	v2.Wallet.Reputation = 5
	v2.SlashCount = 23
	v2.Uptime = 0.897

	got := SelectValidator([]*Validator{v1, v2}, "some-previous-block-hash")
	if got == nil {
		t.Fatalf("expected the bootstrap tier to select a validator even though both have zero stake, got nil (full deadlock)")
	}
}

// TestSelectValidator_ReturnsNilOnlyWhenNoValidatorIsActive confirms
// the bootstrap tier's one real limit: it can't rescue a pool where
// every validator is inactive.
func TestSelectValidator_ReturnsNilOnlyWhenNoValidatorIsActive(t *testing.T) {
	v1 := NewValidator(0)
	v1.Active = false

	got := SelectValidator([]*Validator{v1}, "some-previous-block-hash")
	if got != nil {
		t.Fatalf("expected nil when no validator is active, got %v", got.Wallet.Address)
	}
}

// TestSelectValidator_StrictTierStillPreferredWhenAvailable confirms
// the new bootstrap tier doesn't short-circuit the existing strict/
// relaxed tiers - it's reached only when both are empty.
func TestSelectValidator_StrictTierStillPreferredWhenAvailable(t *testing.T) {
	strict := NewValidator(StakeCap)
	strict.Wallet.Reputation = MaxReputation

	bootstrapOnly := NewValidator(0)
	bootstrapOnly.Wallet.Reputation = 5

	// Run many seeds - the strict-eligible validator must always be
	// the one selected, never the zero-stake one, since the strict
	// pool is non-empty.
	for _, seed := range []string{"a", "b", "c", "d", "e"} {
		got := SelectValidator([]*Validator{strict, bootstrapOnly}, seed)
		if got != strict {
			t.Fatalf("expected the strict-eligible validator to always win while it's available (seed %q)", seed)
		}
	}
}

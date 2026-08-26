package core

import "testing"

// TestDelegationStore_TotalDelegatedByAggregatesAcrossValidators confirms
// TotalDelegatedBy sums a delegator's position across every validator
// they back, not just one - the figure a moderator's stake-floor check
// (api/moderators.go's availableStakedCct) and a wallet's own
// delegated-stake display both actually need.
func TestDelegationStore_TotalDelegatedByAggregatesAcrossValidators(t *testing.T) {
	d := NewDelegationStore()
	d.Delegate("validator-a", "delegator-1", 100)
	d.Delegate("validator-b", "delegator-1", 250)
	d.Delegate("validator-a", "delegator-2", 999) // a different delegator - must not leak in

	if got := d.TotalDelegatedBy("delegator-1"); got != 350 {
		t.Fatalf("expected 350 (100+250) total delegated by delegator-1, got %d", got)
	}
	if got := d.TotalDelegatedBy("delegator-2"); got != 999 {
		t.Fatalf("expected 999 total delegated by delegator-2, got %d", got)
	}
	if got := d.TotalDelegatedBy("nobody"); got != 0 {
		t.Fatalf("expected 0 for an address that's never delegated anything, got %d", got)
	}
}

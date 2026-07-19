package core

import "testing"

// TestReputationTierReward_ReducedValuesPreserveOrdering confirms the
// tier rewards were scaled down (continuous Reputation Credit mining
// is now the dominant REPT source - see api/reputation_mining.go) but
// kept in the same relative order, including the pre-existing
// intentional quirk that Genesis pays less than Diamond (see this
// map's own doc comment in reputation_reward.go).
func TestReputationTierReward_ReducedValuesPreserveOrdering(t *testing.T) {
	want := map[CreditTier]uint64{
		TierSilver:  5,
		TierGold:    20,
		TierDiamond: 75,
		TierGenesis: 50,
	}
	for tier, wantReward := range want {
		if got := reputationTierReward[tier]; got != wantReward {
			t.Fatalf("expected %s reward %d, got %d", tier, wantReward, got)
		}
	}

	if reputationTierReward[TierGenesis] >= reputationTierReward[TierDiamond] {
		t.Fatalf("expected Genesis reward (%d) to stay below Diamond's (%d), same as before the reduction",
			reputationTierReward[TierGenesis], reputationTierReward[TierDiamond])
	}
	if reputationTierReward[TierSilver] >= reputationTierReward[TierGold] {
		t.Fatalf("expected Silver < Gold reward ordering preserved")
	}
}

func TestCumulativeTierReward_UsesReducedValues(t *testing.T) {
	// A wallet jumping straight from Bronze to Gold should get the sum
	// of the reduced Silver+Gold rewards, not the old 50+200=250.
	got := cumulativeTierReward(TierBronze, TierGold)
	want := reputationTierReward[TierSilver] + reputationTierReward[TierGold]
	if got != want {
		t.Fatalf("expected cumulative reward %d (Silver+Gold, reduced values), got %d", want, got)
	}
}

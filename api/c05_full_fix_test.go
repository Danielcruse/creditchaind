package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestVaultLockAttestationID_NewEventUsesContentDerivedID is the direct
// unit test for vaultLockAttestationID's C-05 full fix: with no prior
// record under the legacy ID, two claims for the same external event
// but different recipient/amount must produce DIFFERENT ids - this is
// the property that lets an honest claim and a fraudulent front-run
// naturally diverge instead of colliding.
func TestVaultLockAttestationID_NewEventUsesContentDerivedID(t *testing.T) {
	resetApplyTestState()

	honest := vaultLockAttestData{
		Chain: "ETH", ExternalTxHash: "0xnewevent",
		DepositorCcAddress: "real-depositor", DepositorPublicKey: "real-pubkey",
		AmountSmallestUnit: "1000000000000000000",
	}
	fraudulent := vaultLockAttestData{
		Chain: "ETH", ExternalTxHash: "0xnewevent", // same external event
		DepositorCcAddress: "attacker", DepositorPublicKey: "attacker-pubkey", // different claim
		AmountSmallestUnit: "999000000000000000000",
	}

	honestID := vaultLockAttestationID(honest)
	fraudulentID := vaultLockAttestationID(fraudulent)

	if honestID == fraudulentID {
		t.Fatal("expected differing recipient/amount claims for a brand-new event to produce different IDs")
	}
	legacyID := legacyVaultLockAttestationID("ETH", "0xnewevent", 0)
	if honestID == legacyID || fraudulentID == legacyID {
		t.Fatal("expected a genuinely new event's ID to differ from the legacy chain+tx+index-only ID")
	}
}

// TestVaultLockAttestationID_TwoHonestClaimsConverge confirms every
// validator independently attesting to the SAME real facts computes
// the IDENTICAL id - the property that actually lets honest votes
// accumulate toward threshold.
func TestVaultLockAttestationID_TwoHonestClaimsConverge(t *testing.T) {
	resetApplyTestState()

	claim := vaultLockAttestData{
		Chain: "ETH", ExternalTxHash: "0xconverge",
		DepositorCcAddress: "real-depositor", DepositorPublicKey: "real-pubkey",
		AmountSmallestUnit: "1000000000000000000",
	}
	sameClaimAgain := claim // simulates a second validator independently observing the same event

	if vaultLockAttestationID(claim) != vaultLockAttestationID(sameClaimAgain) {
		t.Fatal("expected two identical claims to converge on the same ID")
	}
}

// TestVaultLockAttestationID_InFlightLegacyEventContinuesOnLegacyID is
// the migration-safety regression test: an event that already has an
// attestation recorded under the OLD (legacy, chain+tx+index-only)
// scheme - e.g. one still resolving from before this fix shipped -
// must keep using that same legacy ID for any FURTHER attestation, not
// split into a new content-derived bucket that would orphan the votes
// already collected. Every node deterministically agrees on this by
// checking the same replicated store, so it never causes disagreement
// about which ID "the" record for an event lives at.
func TestVaultLockAttestationID_InFlightLegacyEventContinuesOnLegacyID(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "legacy-depositor", "legacy-pubkey"

	// Simulates an attestation already recorded under the legacy scheme
	// BEFORE this fix shipped - seeded directly into the store (the
	// same shape a resyncing node would replay historical pre-fix state
	// into), not via attestLock, since a node already running this fix
	// would otherwise have no way to have ever produced a legacy-ID
	// record for a genuinely new event in the first place.
	legacyID := legacyVaultLockAttestationID("ETH", "0xlegacyevent", 0)
	vaultLockAttestationStore.Create(&core.VaultLockAttestation{
		ID:                 legacyID,
		Chain:              "ETH",
		ExternalTxHash:     "0xlegacyevent",
		DepositorCcAddress: depositor,
		DepositorPublicKey: pubKey,
		AmountSmallestUnit: "1000000000000000000",
		Attestors:          map[string]bool{"val-a": true},
		Status:             core.VaultLockPending,
		CreatedAt:          1000,
	})

	// A second validator now attests to the SAME real event, on a node
	// already running this fix - must resolve to the SAME legacy ID
	// (joining the existing record), not a new content-derived one.
	computedID := vaultLockAttestationID(vaultLockAttestData{
		Chain: "ETH", ExternalTxHash: "0xlegacyevent",
		DepositorCcAddress: depositor, DepositorPublicKey: pubKey,
		AmountSmallestUnit: "1000000000000000000",
	})
	if computedID != legacyID {
		t.Fatalf("expected an in-flight legacy event to continue resolving on its original legacy ID %s, got %s", legacyID, computedID)
	}

	attestLock("ETH", "0xlegacyevent", depositor, pubKey, "1000000000000000000", "val-b", 1001)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "ETH_CCC1")
	if got := ethCcc1Store.BalanceOf(depositAddr); got != "1000000000000000000" {
		t.Fatalf("expected the second attestation to join the existing legacy record and reach threshold, got %s", got)
	}
}

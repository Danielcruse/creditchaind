package core

import "testing"

func TestNodeAdmissionStore_GrantIsIdempotentFirstWriteWins(t *testing.T) {
	s := NewNodeAdmissionStore()

	s.Grant(&NodeAdmission{PublicKey: "pk1", NodeID: "first-label", SponsorAddress: "addr-a", ProposalID: "prop-1", GrantedAt: 100})
	s.Grant(&NodeAdmission{PublicKey: "pk1", NodeID: "second-label", SponsorAddress: "addr-b", ProposalID: "prop-2", GrantedAt: 200})

	a, ok := s.Get("pk1")
	if !ok {
		t.Fatalf("expected pk1 to be admitted")
	}
	if a.NodeID != "first-label" || a.SponsorAddress != "addr-a" || a.ProposalID != "prop-1" {
		t.Fatalf("expected the FIRST grant to win, got %+v", a)
	}
}

func TestNodeAdmissionStore_IsAdmitted(t *testing.T) {
	s := NewNodeAdmissionStore()

	if s.IsAdmitted("never-granted") {
		t.Fatalf("expected an ungranted public key to report not admitted")
	}
	s.Grant(&NodeAdmission{PublicKey: "pk1"})
	if !s.IsAdmitted("pk1") {
		t.Fatalf("expected pk1 to report admitted after Grant")
	}
}

func TestNodeAdmissionStore_SnapshotRestoreRoundTrip(t *testing.T) {
	s := NewNodeAdmissionStore()
	s.Grant(&NodeAdmission{PublicKey: "pk1", NodeID: "node-a", SponsorAddress: "addr-a", ProposalID: "prop-1", GrantedAt: 100})
	s.Grant(&NodeAdmission{PublicKey: "pk2", NodeID: "node-b", SponsorAddress: "addr-b", ProposalID: "prop-2", GrantedAt: 200, Grandfathered: true})

	snap := s.Snapshot()

	restored := NewNodeAdmissionStore()
	restored.Restore(snap)

	if !restored.IsAdmitted("pk1") || !restored.IsAdmitted("pk2") {
		t.Fatalf("expected both grants to survive a snapshot/restore round-trip")
	}
	a2, _ := restored.Get("pk2")
	if !a2.Grandfathered {
		t.Fatalf("expected Grandfathered to survive the round-trip, got %+v", a2)
	}

	// Snapshot copies the MAP (a fresh entry added to one doesn't leak
	// into the other), same as BridgeValidatorStore.Snapshot's own
	// convention - it does NOT deep-copy the pointed-to structs, since
	// the only real consumer (JSON serialization to state.json) never
	// mutates them afterward.
	restored.Grant(&NodeAdmission{PublicKey: "pk3"})
	if s.IsAdmitted("pk3") {
		t.Fatalf("expected a new grant on the restored store to NOT leak back into the original store")
	}
}

func TestNodeAdmissionStore_SeedGrandfatheredNodeAdmissionsMarksGrandfathered(t *testing.T) {
	s := NewNodeAdmissionStore()

	s.SeedGrandfatheredNodeAdmissions([]NodeAdmission{
		{PublicKey: "pk1", NodeID: "node1"},
		{PublicKey: "pk2", NodeID: "mainnet"},
	})

	a1, ok := s.Get("pk1")
	if !ok || !a1.Grandfathered {
		t.Fatalf("expected pk1 to be admitted and marked Grandfathered, got %+v (ok=%v)", a1, ok)
	}
	a2, ok := s.Get("pk2")
	if !ok || !a2.Grandfathered {
		t.Fatalf("expected pk2 to be admitted and marked Grandfathered, got %+v (ok=%v)", a2, ok)
	}

	// Seeding again (e.g. a second startup) must not clobber a REAL
	// grant that arrived in the meantime via governance.
	s.Grant(&NodeAdmission{PublicKey: "pk3", SponsorAddress: "real-voter", ProposalID: "prop-real"})
	s.SeedGrandfatheredNodeAdmissions([]NodeAdmission{
		{PublicKey: "pk3", NodeID: "attempted-overwrite"},
	})
	a3, _ := s.Get("pk3")
	if a3.Grandfathered {
		t.Fatalf("expected a real governance-granted admission to never be retroactively marked Grandfathered by reseeding, got %+v", a3)
	}
	if a3.SponsorAddress != "real-voter" {
		t.Fatalf("expected the real grant's fields to survive reseeding untouched, got %+v", a3)
	}
}

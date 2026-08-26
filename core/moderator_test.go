package core

import "testing"

func TestModeratorStore_Deactivate_RemovesFromRotationAndIsIdempotent(t *testing.T) {
	s := NewModeratorStore()
	s.Register("mod-a", "pub-a", 1000)
	s.Register("mod-b", "pub-b", 1001)
	s.Register("mod-c", "pub-c", 1002)

	if !s.Deactivate("mod-b") {
		t.Fatal("expected Deactivate to succeed for a currently-registered moderator")
	}
	m, ok := s.Get("mod-b")
	if !ok || !m.Deactivated {
		t.Fatalf("expected mod-b to be marked Deactivated, got %+v", m)
	}

	// mod-b must never be drawn again, however many times AssignNext runs.
	for i := 0; i < 10; i++ {
		if addr := s.AssignNext(); addr == "mod-b" {
			t.Fatalf("expected a deactivated moderator to never be assigned again (iteration %d)", i)
		}
	}

	// A second Deactivate call on the same address is a no-op, not an error.
	if s.Deactivate("mod-b") {
		t.Fatal("expected a second Deactivate on an already-deactivated moderator to be a no-op")
	}

	// Deactivating an address that was never a moderator is also a safe no-op.
	if s.Deactivate("never-registered") {
		t.Fatal("expected Deactivate to fail for an address that was never a moderator")
	}

	// mod-a and mod-c must still be reachable via AssignNext.
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		seen[s.AssignNext()] = true
	}
	if !seen["mod-a"] || !seen["mod-c"] {
		t.Fatalf("expected mod-a and mod-c to remain in rotation, saw %v", seen)
	}
}

func TestModeratorStatus_Uptime_DefaultsToOneBeforeAnyDisputes(t *testing.T) {
	m := &ModeratorStatus{Address: "fresh-mod"}
	if got := m.Uptime(); got != 1.0 {
		t.Fatalf("expected 1.0 uptime before any dispute has ever been assigned, got %v", got)
	}
}

func TestModeratorStatus_Uptime_IsResolvedOverAssigned(t *testing.T) {
	m := &ModeratorStatus{DisputesAssigned: 4, DisputesResolved: 3}
	if got := m.Uptime(); got != 0.75 {
		t.Fatalf("expected 3/4 = 0.75 uptime, got %v", got)
	}
}

func TestModeratorStore_RecordDisputeAssignedAndResolved(t *testing.T) {
	s := NewModeratorStore()
	s.Register("mod-a", "pub-a", 1000)

	s.RecordDisputeAssigned("mod-a")
	s.RecordDisputeAssigned("mod-a")
	s.RecordDisputeResolved("mod-a")

	m, _ := s.Get("mod-a")
	if m.DisputesAssigned != 2 || m.DisputesResolved != 1 {
		t.Fatalf("expected 2 assigned / 1 resolved, got %+v", m)
	}
	if got := m.Uptime(); got != 0.5 {
		t.Fatalf("expected 1/2 = 0.5 uptime, got %v", got)
	}

	// Both silently no-op for an address that was never registered -
	// same convention as every other ModeratorStore method.
	s.RecordDisputeAssigned("never-registered")
	s.RecordDisputeResolved("never-registered")
	if _, ok := s.Get("never-registered"); ok {
		t.Fatal("expected recording against an unregistered address to create nothing")
	}
}

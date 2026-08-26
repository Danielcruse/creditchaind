package core

import "testing"

func TestStakePurposeStore_LockReleaseLockedOf(t *testing.T) {
	s := NewStakePurposeStore()

	s.Lock("addr1", StakePurposeHub, 3000)
	if got := s.LockedOf("addr1", StakePurposeHub); got != 3000 {
		t.Fatalf("expected 3000 locked for HUB, got %d", got)
	}

	s.Lock("addr1", StakePurposeModeration, 200)
	if got := s.TotalLocked("addr1"); got != 3200 {
		t.Fatalf("expected total locked 3200 across purposes, got %d", got)
	}

	if ok := s.Release("addr1", StakePurposeHub, 500); !ok {
		t.Fatalf("expected release of 500 from HUB to succeed")
	}
	if got := s.LockedOf("addr1", StakePurposeHub); got != 2500 {
		t.Fatalf("expected 2500 remaining locked for HUB after partial release, got %d", got)
	}

	if ok := s.Release("addr1", StakePurposeHub, 100000); ok {
		t.Fatalf("expected release of more than locked to fail")
	}
	if got := s.LockedOf("addr1", StakePurposeHub); got != 2500 {
		t.Fatalf("expected LockedOf unchanged after failed release, got %d", got)
	}
}

func TestStakePurposeStore_LockedOfNilSafe(t *testing.T) {
	s := NewStakePurposeStore()
	if got := s.LockedOf("never-touched", StakePurposeValidatorPool); got != 0 {
		t.Fatalf("expected 0 for an address with no locks, got %d", got)
	}
	if got := s.TotalLocked("never-touched"); got != 0 {
		t.Fatalf("expected 0 total for an address with no locks, got %d", got)
	}
}

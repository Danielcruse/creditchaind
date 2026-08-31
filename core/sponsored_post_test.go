package core

import "testing"

func TestMaxAdSpendForRept(t *testing.T) {
	tests := []struct {
		rept uint64
		want uint64
	}{
		{0, 200},
		{19, 200},
		{20, 2000},
		{99, 2000},
		{100, 10000},
		{499, 10000},
		{500, 50000},
		{10000, 50000},
	}

	for _, tt := range tests {
		if got := MaxAdSpendForRept(tt.rept); got != tt.want {
			t.Errorf("MaxAdSpendForRept(%d) = %d, want %d", tt.rept, got, tt.want)
		}
	}
}

func TestSponsoredPostStore_ReviewApprovalSetsExpiresAtAndActive(t *testing.T) {
	s := NewSponsoredPostStore()
	const moderator = "test-moderator"

	s.Create(&SponsoredPost{ID: "post-1", ModeratorAddress: moderator, Status: SponsoredPostPending})

	if ok := s.Review("post-1", moderator, true, 1000, 5000); !ok {
		t.Fatal("expected review to succeed")
	}

	p, _ := s.Get("post-1")
	if p.Status != SponsoredPostActive {
		t.Fatalf("expected ACTIVE, got %s", p.Status)
	}
	if p.ExpiresAt != 6000 {
		t.Fatalf("expected ExpiresAt 6000 (5000+1000), got %d", p.ExpiresAt)
	}
}

func TestSponsoredPostStore_ReviewRejectionLeavesExpiresAtZero(t *testing.T) {
	s := NewSponsoredPostStore()
	const moderator = "test-moderator"

	s.Create(&SponsoredPost{ID: "post-1", ModeratorAddress: moderator, Status: SponsoredPostPending})

	if ok := s.Review("post-1", moderator, false, 1000, 5000); !ok {
		t.Fatal("expected review to succeed")
	}

	p, _ := s.Get("post-1")
	if p.Status != SponsoredPostRejected {
		t.Fatalf("expected REJECTED, got %s", p.Status)
	}
	if p.ExpiresAt != 0 {
		t.Fatalf("expected ExpiresAt to stay 0 on rejection, got %d", p.ExpiresAt)
	}
}

func TestSponsoredPostStore_ReviewRefusesWrongModeratorOrAlreadyReviewed(t *testing.T) {
	s := NewSponsoredPostStore()
	s.Create(&SponsoredPost{ID: "post-1", ModeratorAddress: "assigned-mod", Status: SponsoredPostPending})

	if ok := s.Review("post-1", "someone-else", true, 1000, 5000); ok {
		t.Fatal("expected review by the wrong moderator to be refused")
	}

	if ok := s.Review("post-1", "assigned-mod", true, 1000, 5000); !ok {
		t.Fatal("expected the assigned moderator's first review to succeed")
	}
	if ok := s.Review("post-1", "assigned-mod", false, 1000, 6000); ok {
		t.Fatal("expected a second review of an already-reviewed post to be refused")
	}
}

func TestSponsoredPostStore_ListActive_FiltersByValidatorStatusAndExpiry(t *testing.T) {
	s := NewSponsoredPostStore()

	s.Create(&SponsoredPost{ID: "global-active", ValidatorID: "", Status: SponsoredPostActive, ExpiresAt: 2000})
	s.Create(&SponsoredPost{ID: "validator-active", ValidatorID: "val-1", Status: SponsoredPostActive, ExpiresAt: 2000})
	s.Create(&SponsoredPost{ID: "pending", ValidatorID: "", Status: SponsoredPostPending})
	s.Create(&SponsoredPost{ID: "expired-already-marked", ValidatorID: "", Status: SponsoredPostExpired, ExpiresAt: 2000})
	s.Create(&SponsoredPost{ID: "active-but-past-expiry", ValidatorID: "", Status: SponsoredPostActive, ExpiresAt: 500})

	global := s.ListActive("", 1000)
	if len(global) != 1 || global[0].ID != "global-active" {
		t.Fatalf("expected only global-active, got %+v", global)
	}

	scoped := s.ListActive("val-1", 1000)
	if len(scoped) != 1 || scoped[0].ID != "validator-active" {
		t.Fatalf("expected only validator-active for val-1, got %+v", scoped)
	}

	noneForOtherValidator := s.ListActive("val-2", 1000)
	if len(noneForOtherValidator) != 0 {
		t.Fatalf("expected no posts for an unrelated validator, got %+v", noneForOtherValidator)
	}
}

func TestSponsoredPostStore_ExpireStale(t *testing.T) {
	s := NewSponsoredPostStore()
	s.Create(&SponsoredPost{ID: "past-due", Status: SponsoredPostActive, ExpiresAt: 500})
	s.Create(&SponsoredPost{ID: "still-good", Status: SponsoredPostActive, ExpiresAt: 2000})

	s.ExpireStale(1000)

	pastDue, _ := s.Get("past-due")
	if pastDue.Status != SponsoredPostExpired {
		t.Fatalf("expected past-due to be EXPIRED, got %s", pastDue.Status)
	}
	stillGood, _ := s.Get("still-good")
	if stillGood.Status != SponsoredPostActive {
		t.Fatalf("expected still-good to remain ACTIVE, got %s", stillGood.Status)
	}
}

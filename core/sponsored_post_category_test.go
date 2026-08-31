package core

import "testing"

func TestIsAllowedSponsoredPostCategory(t *testing.T) {
	if !IsAllowedSponsoredPostCategory(CategoryGeneralBusiness) {
		t.Fatal("expected CategoryGeneralBusiness to be allowed")
	}
	if IsAllowedSponsoredPostCategory(SponsoredPostCategory("NOT_A_REAL_CATEGORY")) {
		t.Fatal("expected a nonsense category to be rejected")
	}
	if IsAllowedSponsoredPostCategory("") {
		t.Fatal("expected an empty category to be rejected")
	}
}

func TestMakesNumericClaims(t *testing.T) {
	tests := []struct {
		category SponsoredPostCategory
		want     bool
	}{
		{CategoryValidatorStaking, true},
		{CategoryLending, true},
		{CategoryTradingHub, true},
		{CategoryGeneralBusiness, false},
		{CategoryCommunity, false},
		{SponsoredPostCategory("NOT_A_REAL_CATEGORY"), false},
	}

	for _, tt := range tests {
		if got := MakesNumericClaims(tt.category); got != tt.want {
			t.Errorf("MakesNumericClaims(%q) = %v, want %v", tt.category, got, tt.want)
		}
	}
}

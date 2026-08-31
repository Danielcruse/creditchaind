package core

import (
	"strings"
	"testing"
)

func TestIsAllowedOffAppCurrency(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"NGN", true},
		{"ngn", true},   // lowercase normalizes
		{" EUR ", true}, // whitespace trims
		{"USD", false},
		{"ZZZ", false}, // not a real currency at all
		{"", false},
	}

	for _, tt := range tests {
		if got := IsAllowedOffAppCurrency(tt.code); got != tt.want {
			t.Errorf("IsAllowedOffAppCurrency(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestIsUSDCurrency(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"USD", true},
		{"usd", true},
		{"US$", true},
		{"$", true},
		{"US Dollar", true},
		{"United States Dollar", true},
		{"NGN", false},
		{"EUR", false},
	}

	for _, tt := range tests {
		if got := IsUSDCurrency(tt.code); got != tt.want {
			t.Errorf("IsUSDCurrency(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestRejectedOffAppCurrencyReason(t *testing.T) {
	if reason := RejectedOffAppCurrencyReason("NGN"); reason != "" {
		t.Fatalf("expected an allowed currency to have no rejection reason, got %q", reason)
	}
	if reason := RejectedOffAppCurrencyReason("USD"); reason == "" {
		t.Fatal("expected USD to be rejected")
	} else if !strings.Contains(strings.ToLower(reason), "money transmission") {
		t.Fatalf("expected the USD rejection reason to name the actual regulatory reason, got %q", reason)
	}
	if reason := RejectedOffAppCurrencyReason("ZZZ"); reason == "" {
		t.Fatal("expected an unsupported currency to be rejected")
	}
}

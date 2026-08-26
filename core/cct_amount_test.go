package core

import (
	"math/big"
	"testing"
)

func TestCctSmallestToDecimalString(t *testing.T) {
	cases := []struct {
		name     string
		smallest *big.Int
		want     string
	}{
		{"nil", nil, "0"},
		{"zero", big.NewInt(0), "0"},
		{"whole number, no fraction", CctWholeToSmallest(5), "5"},
		{"0.3 CCT", new(big.Int).Div(CctWholeToSmallest(3), big.NewInt(10)), "0.3"},
		{"trailing zeros trimmed", new(big.Int).Mul(big.NewInt(150), big.NewInt(1e15)), "0.15"},
		{"whole plus fraction", new(big.Int).Add(CctWholeToSmallest(2), big.NewInt(7)), "2.000000000000000007"},
		{"smallest possible unit", big.NewInt(1), "0.000000000000000001"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CctSmallestToDecimalString(c.smallest); got != c.want {
				t.Fatalf("CctSmallestToDecimalString(%v) = %q, want %q", c.smallest, got, c.want)
			}
		})
	}
}

// TestCctSmallestToDecimalStringRoundTripsThroughCctWholeToSmallest
// checks the formatter against the codebase's own whole-to-smallest
// conversion for a range of values, rather than only hand-picked cases.
func TestCctSmallestToDecimalStringRoundTripsThroughCctWholeToSmallest(t *testing.T) {
	for _, whole := range []uint64{0, 1, 2, 100, 999999} {
		smallest := CctWholeToSmallest(whole)
		got := CctSmallestToDecimalString(smallest)
		want := FormatCctSmallest(big.NewInt(int64(whole)))
		if got != want {
			t.Fatalf("whole=%d: CctSmallestToDecimalString = %q, want %q", whole, got, want)
		}
	}
}

package core

import "testing"

func TestPriceAdjustedInterestRate_NoHistoryReturnsBaseRate(t *testing.T) {
	if got := PriceAdjustedInterestRate(8, nil); got != 8 {
		t.Fatalf("expected base rate 8 with no history, got %d", got)
	}
	if got := PriceAdjustedInterestRate(8, []PricePoint{{Timestamp: 1, Price: 100}}); got != 8 {
		t.Fatalf("expected base rate 8 with a single price point, got %d", got)
	}
}

func TestPriceAdjustedInterestRate_FallingPriceAddsPremium(t *testing.T) {
	// Price fell from 100 to 90 - a 10% drop, well under the cap, so
	// the full 10 points should be added to the base rate.
	history := []PricePoint{
		{Timestamp: 1, Price: 100},
		{Timestamp: 2, Price: 90},
	}
	if got := PriceAdjustedInterestRate(8, history); got != 18 {
		t.Fatalf("expected 8+10=18 for a 10%% price drop, got %d", got)
	}
}

func TestPriceAdjustedInterestRate_RisingPriceAppliesDiscount(t *testing.T) {
	// Price rose from 100 to 110 - a 10% gain, so 10 points should be
	// subtracted from the base rate.
	history := []PricePoint{
		{Timestamp: 1, Price: 100},
		{Timestamp: 2, Price: 110},
	}
	if got := PriceAdjustedInterestRate(20, history); got != 10 {
		t.Fatalf("expected 20-10=10 for a 10%% price rise, got %d", got)
	}
}

func TestPriceAdjustedInterestRate_PremiumCappedAtMax(t *testing.T) {
	// Price crashed 50% - the premium should clamp at
	// PriceRiskPremiumMaxPercent (15), not the full 50.
	history := []PricePoint{
		{Timestamp: 1, Price: 100},
		{Timestamp: 2, Price: 50},
	}
	want := uint64(8 + PriceRiskPremiumMaxPercent)
	if got := PriceAdjustedInterestRate(8, history); got != want {
		t.Fatalf("expected premium capped at %d (rate %d), got %d", PriceRiskPremiumMaxPercent, want, got)
	}
}

func TestPriceAdjustedInterestRate_DiscountFlooredAtMinRate(t *testing.T) {
	// Base rate 3 (Genesis tier) minus a full 15-point discount would
	// go negative - the result must floor at MinInterestRatePercent,
	// never 0 or below.
	history := []PricePoint{
		{Timestamp: 1, Price: 100},
		{Timestamp: 2, Price: 200}, // +100%, discount clamps to -15
	}
	if got := PriceAdjustedInterestRate(3, history); got != MinInterestRatePercent {
		t.Fatalf("expected rate floored at %d, got %d", MinInterestRatePercent, got)
	}
}

func TestPriceAdjustedInterestRate_LookbackUsesOldestWithinWindow(t *testing.T) {
	// More than PriceTrendLookbackPoints samples exist - the
	// comparison should use the point PriceTrendLookbackPoints back
	// from the end, not the very first point ever recorded.
	history := make([]PricePoint, 0, 20)
	for i := 0; i < 20; i++ {
		history = append(history, PricePoint{Timestamp: int64(i), Price: 1000})
	}
	// Move only the most recent PriceTrendLookbackPoints window's
	// starting price down by 10%, leaving older history untouched -
	// only this recent window should be visible to the function.
	history[len(history)-PriceTrendLookbackPoints] = PricePoint{Timestamp: 100, Price: 1000}
	history[len(history)-1] = PricePoint{Timestamp: 101, Price: 900}

	if got := PriceAdjustedInterestRate(8, history); got != 18 {
		t.Fatalf("expected the lookback window's 10%% drop (rate 18), got %d", got)
	}
}

package core

import "testing"

// TestLiquidationThresholdPercentFor confirms the per-symbol override
// table: STETH_CCC1 gets its own stricter margin, every other symbol
// (including ones with no entry at all) falls back to the shared
// LiquidationThresholdPercent default.
func TestLiquidationThresholdPercentFor(t *testing.T) {
	if got := LiquidationThresholdPercentFor("STETH_CCC1"); got != StethCcc1LiquidationThresholdPercent {
		t.Fatalf("expected STETH_CCC1 to use its own threshold (%d), got %d", StethCcc1LiquidationThresholdPercent, got)
	}
	if got := LiquidationThresholdPercentFor("OETH"); got != LiquidationThresholdPercent {
		t.Fatalf("expected OETH to use the default threshold (%d), got %d", LiquidationThresholdPercent, got)
	}
	if got := LiquidationThresholdPercentFor("CCT"); got != LiquidationThresholdPercent {
		t.Fatalf("expected CCT to use the default threshold (%d), got %d", LiquidationThresholdPercent, got)
	}
	if got := LiquidationThresholdPercentFor("SOME_FUTURE_SYMBOL"); got != LiquidationThresholdPercent {
		t.Fatalf("expected an unlisted symbol to fall back to the default threshold (%d), got %d", LiquidationThresholdPercent, got)
	}
}

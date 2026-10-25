package api

import (
	"testing"
	"time"
)

func resetPriceOracleTestState() {
	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{}
	priceOracleMu.Unlock()
}

func TestAcceptPriceUpdate_FirstReadingAlwaysAccepted(t *testing.T) {
	if got := acceptPriceUpdate(0, 3000); got != 3000 {
		t.Fatalf("expected a first reading (no baseline) to be accepted outright, got %v", got)
	}
}

func TestAcceptPriceUpdate_NormalMoveAccepted(t *testing.T) {
	// A 10% move is well within the 20% threshold.
	if got := acceptPriceUpdate(3000, 3300); got != 3300 {
		t.Fatalf("expected a normal 10%% move to be accepted, got %v", got)
	}
}

func TestAcceptPriceUpdate_RejectsSwingPastThreshold(t *testing.T) {
	// A >20% single-tick jump must be rejected - old value kept.
	if got := acceptPriceUpdate(3000, 3700); got != 3000 {
		t.Fatalf("expected a >20%% swing to be rejected and the old value kept, got %v", got)
	}
	// Works symmetrically for a crash, not just a spike.
	if got := acceptPriceUpdate(3000, 2000); got != 3000 {
		t.Fatalf("expected a >20%% downward swing to be rejected and the old value kept, got %v", got)
	}
}

func TestAcceptPriceUpdate_AcceptsExactlyAtThreshold(t *testing.T) {
	if got := acceptPriceUpdate(1000, 1200); got != 1200 {
		t.Fatalf("expected exactly 20%% to be accepted (not just under), got %v", got)
	}
}

func TestAcceptPriceUpdate_ZeroNewValueKeepsOld(t *testing.T) {
	if got := acceptPriceUpdate(3000, 0); got != 3000 {
		t.Fatalf("expected a zero/missing new reading to never overwrite a real old value, got %v", got)
	}
}

func TestMergePriceRates_RejectsSwingPerFieldIndependently(t *testing.T) {
	old := map[string]currencyRates{
		"USD": {Eth: 3000, Btc: 60000, Usdc: 1.0},
	}
	// ETH swings >20% (bad reading), BTC moves normally, USDC repeats -
	// each field's accept/reject decision must be independent.
	next := map[string]currencyRates{
		"USD": {Eth: 10000, Btc: 61000, Usdc: 1.0},
	}

	merged := mergePriceRates(old, next)
	got := merged["USD"]
	if got.Eth != 3000 {
		t.Fatalf("expected ETH's bad swing rejected (kept at 3000), got %v", got.Eth)
	}
	if got.Btc != 61000 {
		t.Fatalf("expected BTC's normal move accepted, got %v", got.Btc)
	}
	if got.Usdc != 1.0 {
		t.Fatalf("expected USDC unchanged, got %v", got.Usdc)
	}
}

func TestMergePriceRates_CurrencyMissingFromUpdateKeepsOldValue(t *testing.T) {
	// Simulates a Coinbase-fallback tick, which only ever reports USD -
	// every other currency must be carried forward unchanged.
	old := map[string]currencyRates{
		"USD": {Eth: 3000, Btc: 60000, Usdc: 1.0},
		"EUR": {Eth: 2800, Btc: 56000, Usdc: 0.92},
	}
	next := map[string]currencyRates{
		"USD": {Eth: 3100, Btc: 61000, Usdc: 1.0},
	}

	merged := mergePriceRates(old, next)
	if merged["EUR"] != old["EUR"] {
		t.Fatalf("expected EUR to be carried forward unchanged during a USD-only update, got %+v", merged["EUR"])
	}
	if merged["USD"].Eth != 3100 {
		t.Fatalf("expected USD to actually update, got %+v", merged["USD"])
	}
}

func TestComputeFairValueSwap_RefusesOnStaleCache(t *testing.T) {
	resetPriceOracleTestState()

	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{
		Rates:     map[string]currencyRates{"USD": {Eth: 3000 * 1e18, Btc: 60000 * 1e8, Usdc: 1.0}},
		UpdatedAt: time.Now().Unix() - priceStalenessSeconds - 1,
	}
	priceOracleMu.Unlock()

	_, _, _, err := computeFairValueSwap("ETHB", "BTCB", 1_000_000_000_000_000_000)
	if err == nil {
		t.Fatalf("expected a stale cache to refuse the swap outright")
	}
}

func TestComputeFairValueSwap_NeverPolledIsStale(t *testing.T) {
	resetPriceOracleTestState()

	_, _, _, err := computeFairValueSwap("ETHB", "BTCB", 1_000_000_000_000_000_000)
	if err == nil {
		t.Fatalf("expected a never-polled (zero UpdatedAt) cache to refuse the swap outright")
	}
}

func TestComputeFairValueSwap_FreshCacheSucceeds(t *testing.T) {
	resetPriceOracleTestState()

	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{
		Rates:     map[string]currencyRates{"USD": {Eth: 3000, Btc: 60000, Usdc: 1.0}},
		UpdatedAt: time.Now().Unix(),
	}
	priceOracleMu.Unlock()

	// 1 ETH (in wei) -> BTCB (in sats): usdValue = 3000, at 60000/BTC ->
	// 0.05 BTC = 5,000,000 sats.
	toAmount, usdValue, _, err := computeFairValueSwap("ETHB", "BTCB", 1_000_000_000_000_000_000)
	if err != nil {
		t.Fatalf("expected a fresh cache to allow the swap, got error: %v", err)
	}
	if usdValue != 3000 {
		t.Fatalf("expected usdValue 3000, got %v", usdValue)
	}
	if toAmount != 5_000_000 {
		t.Fatalf("expected 5,000,000 sats, got %d", toAmount)
	}
}

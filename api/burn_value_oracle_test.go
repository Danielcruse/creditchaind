package api

import (
	"math/big"
	"testing"
)

func TestUsdCentsToCctWhole_BasicMath(t *testing.T) {
	// $5.00 (500 cents) at 10 cents/CCT -> 50 CCT.
	if got := usdCentsToCctWhole(big.NewInt(500), 10); got != 50 {
		t.Fatalf("expected 50 CCT, got %d", got)
	}
}

func TestUsdCentsToCctWhole_FailsOpenOnUnsetRate(t *testing.T) {
	if got := usdCentsToCctWhole(big.NewInt(500), 0); got != 0 {
		t.Fatalf("expected 0 with an unset CCT/USD rate, got %d", got)
	}
}

func TestUsdCentsToCctWhole_FailsOpenOnNonPositiveCents(t *testing.T) {
	if got := usdCentsToCctWhole(big.NewInt(0), 10); got != 0 {
		t.Fatalf("expected 0 for zero cents, got %d", got)
	}
	if got := usdCentsToCctWhole(big.NewInt(-5), 10); got != 0 {
		t.Fatalf("expected 0 for negative cents, got %d", got)
	}
}

func TestCctEquivalentWholeOfBurn_EthCcc1(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents":       10,      // $0.10/CCT
		"EthUsdCentsPerEth": 200_000, // $2,000.00/ETH
	})

	// 0.01 ETH (1e16 wei) at $2,000/ETH = $20.00 = 2000 cents -> 200 CCT.
	got := cctEquivalentWholeOfBurn("ETH_CCC1", 10_000_000_000_000_000)
	if got != 200 {
		t.Fatalf("expected 200 CCT, got %d", got)
	}
}

func TestCctEquivalentWholeOfBurn_BtcCcc1(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents":       10,        // $0.10/CCT
		"BtcUsdCentsPerBtc": 5_000_000, // $50,000.00/BTC
	})

	// 0.001 BTC (100,000 sats) at $50,000/BTC = $50.00 = 5000 cents -> 500 CCT.
	got := cctEquivalentWholeOfBurn("BTC_CCC1", 100_000)
	if got != 500 {
		t.Fatalf("expected 500 CCT, got %d", got)
	}
}

func TestCctEquivalentWholeOfBurn_UsdcCcc1_PeggedOneToOne(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents": 10, // $0.10/CCT
	})

	// 100 USDC_CCC1 (100 * 1e6 smallest units) = $100.00 = 10,000 cents -> 1,000 CCT.
	got := cctEquivalentWholeOfBurn("USDC_CCC1", 100_000_000)
	if got != 1_000 {
		t.Fatalf("expected 1,000 CCT, got %d", got)
	}
}

func TestCctEquivalentWholeOfBurn_ZeroWhenCctRateUnset(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"EthUsdCentsPerEth": 200_000,
	})

	if got := cctEquivalentWholeOfBurn("ETH_CCC1", 10_000_000_000_000_000); got != 0 {
		t.Fatalf("expected 0 with CctUsdCents unset, got %d", got)
	}
}

func TestCctEquivalentWholeOfBurn_ZeroWhenSymbolRateUnset(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents": 10,
	})

	if got := cctEquivalentWholeOfBurn("ETH_CCC1", 10_000_000_000_000_000); got != 0 {
		t.Fatalf("expected 0 with EthUsdCentsPerEth unset, got %d", got)
	}
	if got := cctEquivalentWholeOfBurn("BTC_CCC1", 100_000); got != 0 {
		t.Fatalf("expected 0 with BtcUsdCentsPerBtc unset, got %d", got)
	}
}

// TestCctEquivalentWholeOfBurn_ReptHasNoDefinedRate documents the
// disclosed limitation - REPT is a reputation-progression token, not
// a priced asset, and has no USD/CCT exchange rate anywhere in this
// codebase, regardless of what governance params are set.
func TestCctEquivalentWholeOfBurn_ReptHasNoDefinedRate(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents": 10,
	})

	if got := cctEquivalentWholeOfBurn("REPT", 1_000_000); got != 0 {
		t.Fatalf("expected 0 for REPT (no defined rate), got %d", got)
	}
}

func TestCctEquivalentWholeOfBurn_ZeroAmount(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents":       10,
		"EthUsdCentsPerEth": 200_000,
	})

	if got := cctEquivalentWholeOfBurn("ETH_CCC1", 0); got != 0 {
		t.Fatalf("expected 0 for a zero-amount burn, got %d", got)
	}
}

// TestSplitPlatformFee_NonCctBurnFeedsPendingRewardViaValuation covers
// the full integration: a BTC_CCC1 platform-fee burn, with both rates
// configured, adds its CCT-equivalent value to
// core.Treasury.CCTBurnedPendingReward - and does so purely as a
// valuation, never by minting/burning/moving any CCT balance.
func TestSplitPlatformFee_NonCctBurnFeedsPendingRewardViaValuation(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"
	setGovernanceParamsForTest(t, map[string]uint64{
		"CctUsdCents":       10,        // $0.10/CCT
		"BtcUsdCentsPerBtc": 5_000_000, // $50,000.00/BTC
	})

	const from = "fee-source"
	store, err := resolveTokenStore("BTC_CCC1")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	store.Mint(from, 10_000_000) // 0.1 BTC_CCC1

	// fee = 1,000,000 sats (0.01 BTC); burnShare = 500,000 sats (0.005
	// BTC) = $250.00 = 25,000 cents -> 2,500 CCT-equivalent.
	splitPlatformFee(store, "BTC_CCC1", from, 1_000_000, "")

	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 2_500 {
		t.Fatalf("expected CCTBurnedPendingReward to gain 2,500 CCT-equivalent, got %d", got)
	}
	// The real BTC_CCC1 burn still happened in its own currency - no
	// CCT was actually minted or moved by this valuation.
	if got := cctStore.BalanceOf(from); got != "0" {
		t.Fatalf("expected no CCT balance change from a BTC_CCC1 burn's valuation, got %s", got)
	}
}

// TestSplitPlatformFee_NonCctBurnNoOpWithoutRatesConfigured covers the
// fail-open path end to end: the BTC_CCC1 burn itself still happens
// exactly as before, but with no rates configured the reward
// accumulator is untouched rather than erroring or blocking the fee
// split.
func TestSplitPlatformFee_NonCctBurnNoOpWithoutRatesConfigured(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const from = "fee-source"
	store, err := resolveTokenStore("BTC_CCC1")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	store.Mint(from, 10_000_000)

	splitPlatformFee(store, "BTC_CCC1", from, 1_000_000, "")

	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 0 {
		t.Fatalf("expected CCTBurnedPendingReward to stay 0 with no rates configured, got %d", got)
	}
	if got := store.BalanceOf(from); got != 9_000_000 {
		t.Fatalf("expected the real BTC_CCC1 fee deduction to still happen (1,000,000 sats), got %d left (want 9,000,000)", got)
	}
}

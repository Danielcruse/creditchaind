package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestSplitPlatformFee_SplitsFiftyTwentyTenTenTen covers the current
// fee-review decision: the marketplace platform fee (0.3% of a trade,
// core.PlatformFeeAmount) is split 50% burned / 20% into a per-symbol
// validator pool / 10% Foundation Treasury (admin) / 10% Outernet
// operator / 10% moderator pool (this call passes no resolving
// moderator, so it pools rather than paying anyone directly).
func TestSplitPlatformFee_SplitsFiftyTwentyTenTenTen(t *testing.T) {
	resetApplyTestState()
	reptStore = core.NewTokenStore()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const from = "fee-source"
	const symbol = "REPT"
	store, err := resolveTokenStore(symbol)
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	store.Mint(from, 1000)

	splitPlatformFee(store, symbol, from, 100, "") // a clean round number: 50/20/10/10/10

	if got := store.BalanceOf(from); got != 900 {
		t.Fatalf("expected source balance to drop by the full fee (100), got %d left (want 900)", got)
	}
	if got := store.BalanceOf(platformFeeValidatorPoolAddress(symbol)); got != 20 {
		t.Fatalf("expected validator pool to receive 20, got %d", got)
	}
	if got := store.BalanceOf(treasuryStore.Treasury.FoundationAddress); got != 10 {
		t.Fatalf("expected Foundation Treasury (admin share) to receive 10, got %d", got)
	}
	if got := store.BalanceOf(outernetOperatorAddress); got != 10 {
		t.Fatalf("expected the Outernet operator address to receive 10, got %d", got)
	}
	if got := store.BalanceOf(platformFeeModeratorPoolAddress(symbol)); got != 10 {
		t.Fatalf("expected the moderator pool to receive 10 (no resolving moderator passed), got %d", got)
	}
	// The remaining 50 must not be sitting anywhere retrievable - it
	// was genuinely burned (core.TokenStore.Burn), not parked at an
	// unclaimed-but-still-extant address the way other fee sinks in
	// this codebase (registrationFeeAddress etc.) work.
	accountedFor := store.BalanceOf(platformFeeValidatorPoolAddress(symbol)) +
		store.BalanceOf(treasuryStore.Treasury.FoundationAddress) +
		store.BalanceOf(outernetOperatorAddress) +
		store.BalanceOf(platformFeeModeratorPoolAddress(symbol))
	if accountedFor != 50 {
		t.Fatalf("expected exactly 50 of the 100 fee to be accounted for (validator+admin+outernet+moderator), got %d - the other 50 should be burned, not sitting somewhere", accountedFor)
	}
}

// TestSplitPlatformFee_ResolvingModeratorPaysDirectlyInsteadOfPool
// confirms a non-empty resolvingModerator gets the moderator share
// directly, and nothing lands in the pool for that same call.
func TestSplitPlatformFee_ResolvingModeratorPaysDirectlyInsteadOfPool(t *testing.T) {
	resetApplyTestState()
	reptStore = core.NewTokenStore()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const from = "fee-source"
	const symbol = "REPT"
	const moderator = "resolving-moderator-addr"
	store, _ := resolveTokenStore(symbol)
	store.Mint(from, 1000)

	splitPlatformFee(store, symbol, from, 100, moderator)

	if got := store.BalanceOf(moderator); got != 10 {
		t.Fatalf("expected the resolving moderator to receive 10 directly, got %d", got)
	}
	if got := store.BalanceOf(platformFeeModeratorPoolAddress(symbol)); got != 0 {
		t.Fatalf("expected nothing left in the moderator pool for this call, got %d", got)
	}
}

// TestSplitPlatformFee_ZeroFeeIsANoOp confirms a fee too small to
// produce any share (e.g. 0.3% of a tiny trade, floored to 0 by
// integer math) touches no balance at all - matches
// TestApplyHubTradeRequest_OnChain_BTCB's real-world case (100 sats).
func TestSplitPlatformFee_ZeroFeeIsANoOp(t *testing.T) {
	resetApplyTestState()
	reptStore = core.NewTokenStore()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const from = "fee-source-2"
	const symbol = "REPT"
	store, _ := resolveTokenStore(symbol)
	store.Mint(from, 1000)

	splitPlatformFee(store, symbol, from, 0, "")

	if got := store.BalanceOf(from); got != 1000 {
		t.Fatalf("expected source balance untouched at 1000, got %d", got)
	}
}

// TestApplyBlockReward_SweepsPlatformFeeValidatorPoolForEthCcc1 checks
// the sweep side for a non-CCT native symbol specifically - the CCT
// transfer-fee pool sweep already had coverage assumptions baked into
// older tests, but this is the first coverage of sweeping a DIFFERENT
// symbol's platform-fee validator pool (see api/apply.go's
// applyBlockReward), which the marketplace fee now needs since P2P
// trades aren't CCT-only.
func TestApplyBlockReward_SweepsPlatformFeeValidatorPoolForEthCcc1(t *testing.T) {
	resetApplyTestState()

	const validator = "block-validator-addr"
	nonAddressValidators[validator] = false

	ethCcc1Store.Mint(platformFeeValidatorPoolAddress("ETH_CCC1"), "5000000000000000") // 0.005 ETH

	applyBlockReward(core.Block{Validator: validator})

	if got := ethCcc1Store.BalanceOf(validator); got != "5000000000000000" {
		t.Fatalf("expected validator to receive the swept ETH_CCC1 pool, got %s", got)
	}
	if got := ethCcc1Store.BalanceOf(platformFeeValidatorPoolAddress("ETH_CCC1")); got != "0" {
		t.Fatalf("expected the pool itself to be emptied after sweeping, got %s left", got)
	}
}

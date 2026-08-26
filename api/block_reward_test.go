package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestSplitPlatformFee_CCTBurnFeedsPendingReward covers the fee-burn-
// based block reward's accumulation side: a CCT platform-fee burn adds
// its full burnShare to core.Treasury.CCTBurnedPendingReward, on top
// of the real core.Burn already covered by
// TestSplitPlatformFee_SplitsFiftyTwentyTenTenTen.
func TestSplitPlatformFee_CCTBurnFeedsPendingReward(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const from = "fee-source"
	store, err := resolveTokenStore("CCT")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	store.Mint(from, 1000)

	splitPlatformFee(store, "CCT", from, 100, "") // burnShare = 50

	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 50 {
		t.Fatalf("expected CCTBurnedPendingReward to accumulate the 50 CCT burn share, got %d", got)
	}

	splitPlatformFee(store, "CCT", from, 100, "") // a second fee cycle accumulates on top

	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 100 {
		t.Fatalf("expected CCTBurnedPendingReward to accumulate across multiple burns (50+50), got %d", got)
	}
}

// TestSplitPlatformFee_NonCCTBurnDoesNotFeedPendingReward covers the
// deliberate asymmetry: a non-CCT symbol's burn share still reduces
// that symbol's own supply, but must NOT feed
// core.Treasury.CCTBurnedPendingReward - there's no defined exchange
// rate to convert e.g. a REPT burn into a CCT-denominated reward.
func TestSplitPlatformFee_NonCCTBurnDoesNotFeedPendingReward(t *testing.T) {
	resetApplyTestState()
	reptStore = core.NewTokenStore()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"

	const from = "fee-source"
	store, err := resolveTokenStore("REPT")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	store.Mint(from, 1000)

	splitPlatformFee(store, "REPT", from, 100, "")

	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 0 {
		t.Fatalf("expected a REPT burn to leave CCTBurnedPendingReward untouched, got %d", got)
	}
}

// TestPendingBurnBasedBlockReward_IsNinetyPercentOfAccumulator covers
// the exact 90% math, and that the preview helper never itself
// mutates the accumulator.
func TestPendingBurnBasedBlockReward_IsNinetyPercentOfAccumulator(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.CCTBurnedPendingReward = 30

	if got := pendingBurnBasedBlockReward(); got != 27 {
		t.Fatalf("expected 90%% of 30 = 27, got %d", got)
	}
	if treasuryStore.Treasury.CCTBurnedPendingReward != 30 {
		t.Fatalf("expected pendingBurnBasedBlockReward to be read-only, accumulator changed to %d", treasuryStore.Treasury.CCTBurnedPendingReward)
	}
}

// TestPendingBurnBasedBlockReward_ZeroWhenNoBurnsOccurred covers a
// block produced with no fee-burn activity since the last one - the
// old flat/decaying reward always paid something regardless of
// activity; the new mechanism correctly pays zero.
func TestPendingBurnBasedBlockReward_ZeroWhenNoBurnsOccurred(t *testing.T) {
	resetApplyTestState()

	if got := pendingBurnBasedBlockReward(); got != 0 {
		t.Fatalf("expected 0 reward with no accumulated burns, got %d", got)
	}
}

// TestApplyBlockReward_ResetsAccumulatorAfterPayout covers that
// applyBlockReward both pays pendingBurnBasedBlockReward() and resets
// the accumulator to 0, so a subsequent block with no new burn
// activity correctly pays zero rather than double-paying.
func TestApplyBlockReward_ResetsAccumulatorAfterPayout(t *testing.T) {
	resetApplyTestState()

	const validator = "block-reward-reset-validator"
	treasuryStore.Treasury.CCTBurnedPendingReward = 20 // -> 18 CCT reward

	applyBlockReward(core.Block{Validator: validator})

	if got := cctStore.BalanceOf(validator); got != cctW(18) {
		t.Fatalf("expected validator to receive 90%% of the 20 CCT accumulator (18), got %s", got)
	}
	if treasuryStore.Treasury.CCTBurnedPendingReward != 0 {
		t.Fatalf("expected accumulator reset to 0 after payout, got %d", treasuryStore.Treasury.CCTBurnedPendingReward)
	}

	// A second block with no new burn activity since the reset must
	// pay zero additional reward.
	applyBlockReward(core.Block{Validator: validator})

	if got := cctStore.BalanceOf(validator); got != cctW(18) {
		t.Fatalf("expected no additional reward on a block with no new burn activity, balance changed to %s", got)
	}
}

// TestApplyBlockReward_SplitsBurnBasedRewardWithDelegators covers that
// the burn-based reward, like the old flat one, still splits between
// validator commission and pro-rata delegator shares once a real
// delegation pool exists behind the validator.
func TestApplyBlockReward_SplitsBurnBasedRewardWithDelegators(t *testing.T) {
	resetApplyTestState()

	const validator = "block-reward-delegated-validator"
	const delegator = "block-reward-delegator"
	treasuryStore.Treasury.CCTBurnedPendingReward = 100 // -> 90 CCT reward

	validatorStore.Register(validator, "node-id", "node-pubkey")
	status, _ := validatorStore.Get(validator)
	status.CommissionBps = 1000 // 10%
	delegationStore.Delegations[validator] = map[string]uint64{delegator: 50}

	applyBlockReward(core.Block{Validator: validator})

	// commission = 90 * 10% = 9, poolShare = 81, all to the one delegator.
	if got := cctStore.BalanceOf(validator); got != cctW(9) {
		t.Fatalf("expected validator commission of 9 CCT, got %s", got)
	}
	if got := cctStore.BalanceOf(delegator); got != cctW(81) {
		t.Fatalf("expected delegator to receive the full 81 CCT pool share, got %s", got)
	}
}

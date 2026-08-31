package api

import "testing"

func resetAdFeeSplitTestState() {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "ad-fee-test-foundation"
}

func TestSplitAdFee_ValidatorScoped_PaysAllFiveDestinations(t *testing.T) {
	resetAdFeeSplitTestState()

	const advertiser = "ad-fee-test-advertiser"
	const validator = "ad-fee-test-validator"

	store, err := resolveTokenStore("CCT")
	if err != nil {
		t.Fatalf("resolveTokenStore(CCT): %v", err)
	}
	store.Mint(advertiser, 10000)

	splitAdFee(store, advertiser, validator, 10000)

	// Defaults: Burn 4000bps / Validator 3000bps / Treasury 1500bps /
	// Moderator 1000bps / GovFund 500bps of 10000 fee.
	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 4000 {
		t.Fatalf("expected 4000 burned into pending reward, got %d", got)
	}
	if got := store.BalanceOf(validator); got != 3000 {
		t.Fatalf("expected validator share 3000, got %d", got)
	}
	if got := store.BalanceOf(treasuryStore.Treasury.FoundationAddress); got != 1500 {
		t.Fatalf("expected treasury share 1500, got %d", got)
	}
	if got := store.BalanceOf(platformFeeModeratorPoolAddress("CCT")); got != 1000 {
		t.Fatalf("expected moderator pool share 1000, got %d", got)
	}
	if got := store.BalanceOf(adFeeGovFundAddress()); got != 500 {
		t.Fatalf("expected governance fund share 500, got %d", got)
	}
	if got := store.BalanceOf(advertiser); got != 0 {
		t.Fatalf("expected advertiser fully drained (10000 fee, 10000 minted), got %d remaining", got)
	}
}

// TestSplitAdFee_GlobalReach_ValidatorShareRollsIntoGovFund confirms a
// global post (validatorID == "") doesn't silently drop the validator
// share - it rolls into the governance fund instead of vanishing.
func TestSplitAdFee_GlobalReach_ValidatorShareRollsIntoGovFund(t *testing.T) {
	resetAdFeeSplitTestState()

	const advertiser = "ad-fee-test-advertiser-global"

	store, err := resolveTokenStore("CCT")
	if err != nil {
		t.Fatalf("resolveTokenStore(CCT): %v", err)
	}
	store.Mint(advertiser, 10000)

	splitAdFee(store, advertiser, "", 10000)

	// GovFund now absorbs both its own 500bps AND the validator's
	// 3000bps (nobody to pay) = 3500.
	if got := store.BalanceOf(adFeeGovFundAddress()); got != 3500 {
		t.Fatalf("expected governance fund to absorb its own share plus the unpaid validator share (3500), got %d", got)
	}
	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 4000 {
		t.Fatalf("expected burn share unaffected by global reach, got %d", got)
	}
	if got := store.BalanceOf(treasuryStore.Treasury.FoundationAddress); got != 1500 {
		t.Fatalf("expected treasury share unaffected by global reach, got %d", got)
	}
	if got := store.BalanceOf(platformFeeModeratorPoolAddress("CCT")); got != 1000 {
		t.Fatalf("expected moderator share unaffected by global reach, got %d", got)
	}
}

func TestSplitAdFee_ZeroFeeIsNoOp(t *testing.T) {
	resetAdFeeSplitTestState()

	store, err := resolveTokenStore("CCT")
	if err != nil {
		t.Fatalf("resolveTokenStore(CCT): %v", err)
	}

	splitAdFee(store, "someone", "some-validator", 0)

	if got := treasuryStore.Treasury.CCTBurnedPendingReward; got != 0 {
		t.Fatalf("expected no burn for a zero fee, got %d", got)
	}
}

package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func resetModeratorParamsGovernanceTestState() {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()
	governanceParamsStore = core.NewGovernanceParamsStore()
	stakePurposeStore = core.NewStakePurposeStore()
}

func TestModeratorRegisterFeeCCT_DefaultsThenRespectsGovernanceOverride(t *testing.T) {
	resetModeratorParamsGovernanceTestState()

	if got := moderatorRegisterFeeCCT(); got != ModeratorRegisterFeeCCT {
		t.Fatalf("expected default fee %d, got %d", ModeratorRegisterFeeCCT, got)
	}

	governanceParamsStore.Set(moderatorFeeParamKey, 77)
	if got := moderatorRegisterFeeCCT(); got != 77 {
		t.Fatalf("expected overridden fee 77, got %d", got)
	}
}

func TestModeratorStakeRequirementCCT_DefaultsThenRespectsGovernanceOverride(t *testing.T) {
	resetModeratorParamsGovernanceTestState()

	if got := moderatorStakeRequirementCCT(); got != DefaultModeratorStakeRequirementCCT {
		t.Fatalf("expected default stake requirement %d, got %d", DefaultModeratorStakeRequirementCCT, got)
	}

	governanceParamsStore.Set(moderatorStakeParamKey, 555)
	if got := moderatorStakeRequirementCCT(); got != 555 {
		t.Fatalf("expected overridden stake requirement 555, got %d", got)
	}
}

func TestApplyModeratorRegister_LocksModerationPurposeStake(t *testing.T) {
	resetModeratorParamsGovernanceTestState()

	const addr = "mod-lock-test"
	walletStore.Get(addr).Reputation = 50
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT + 50}
	cctStore.Mint(addr, cctW(ModeratorRegisterFeeCCT))

	applyModeratorRegister(core.Transaction{ID: "mod-lock-test-tx", From: addr, Type: core.TxModeratorRegister, Timestamp: 1000})

	if !moderatorStore.IsModerator(addr) {
		t.Fatalf("expected address to be registered as a moderator")
	}
	if got := stakePurposeStore.LockedOf(addr, core.StakePurposeModeration); got != DefaultModeratorStakeRequirementCCT {
		t.Fatalf("expected exactly %d CCT locked for MODERATION, got %d", DefaultModeratorStakeRequirementCCT, got)
	}
}

func TestApplyModeratorRegister_LocksOnlyWhatsAvailableWhenPartiallyLockedElsewhere(t *testing.T) {
	resetModeratorParamsGovernanceTestState()

	const addr = "mod-lock-partial-test"
	walletStore.Get(addr).Reputation = 50
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT}
	// Half of this wallet's stake is already locked for an unrelated
	// purpose - only the remainder should end up locked for MODERATION,
	// same "lock what's available, don't fail the whole action"
	// precedent as applyCompanyReview's hub stake vault.
	stakePurposeStore.Lock(addr, core.StakePurposeHub, DefaultModeratorStakeRequirementCCT/2)
	cctStore.Mint(addr, cctW(ModeratorRegisterFeeCCT))

	applyModeratorRegister(core.Transaction{ID: "mod-lock-partial-test-tx", From: addr, Type: core.TxModeratorRegister, Timestamp: 1000})

	if !moderatorStore.IsModerator(addr) {
		t.Fatalf("expected address to be registered as a moderator even with a partial lock")
	}
	if got := stakePurposeStore.LockedOf(addr, core.StakePurposeModeration); got != DefaultModeratorStakeRequirementCCT/2 {
		t.Fatalf("expected only the remaining %d CCT locked for MODERATION, got %d", DefaultModeratorStakeRequirementCCT/2, got)
	}
}

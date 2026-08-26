package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func resetModeratorDelegationTestState() {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()
	delegationStore = core.NewDelegationStore()
	validatorStore = core.NewValidatorStore()
}

// TestAvailableStakedCct_SubtractsDelegatedOut is a direct test of the
// helper the moderator floor check is built on: staking and delegating
// are separate pools (see core.DelegationStore's doc comment), so
// "available" staked CCT must be staked minus whatever's been sent out
// via delegation, not raw stakingStore.StakeOf.
func TestAvailableStakedCct_SubtractsDelegatedOut(t *testing.T) {
	resetModeratorDelegationTestState()

	const addr = "avail-test-addr"
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 3020}
	delegationStore.Delegate("some-validator", addr, 3000)

	if got := availableStakedCct(addr); got != 20 {
		t.Fatalf("expected 3020-3000=20 available, got %d", got)
	}
}

// TestActiveModeratorStakeShortfall_ExemptsNonModeratorsAndDeactivated
// confirms the floor only ever applies to a currently-active,
// registered moderator - anyone else (never registered, or registered
// and later deactivated) has no shortfall regardless of their stake.
func TestActiveModeratorStakeShortfall_ExemptsNonModeratorsAndDeactivated(t *testing.T) {
	resetModeratorDelegationTestState()

	const nonModerator = "never-a-moderator"
	if got := activeModeratorStakeShortfall(nonModerator, 1_000_000); got != 0 {
		t.Fatalf("expected 0 shortfall for a non-moderator regardless of amount, got %d", got)
	}

	const deactivated = "deactivated-moderator"
	moderatorStore.Register(deactivated, "pubkey", 1000)
	moderatorStore.Deactivate(deactivated)
	stakingStore.Stakes[deactivated] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT}
	if got := activeModeratorStakeShortfall(deactivated, DefaultModeratorStakeRequirementCCT); got != 0 {
		t.Fatalf("expected 0 shortfall for a deactivated moderator, got %d", got)
	}
}

// TestActiveModeratorStakeShortfall_BlocksDelegationBelowFloor is the
// core regression test for the user-reported gap: an active moderator
// delegating away staked CCT must not be allowed to drop their
// available balance below their own registration requirement.
func TestActiveModeratorStakeShortfall_BlocksDelegationBelowFloor(t *testing.T) {
	resetModeratorDelegationTestState()

	const addr = "active-moderator"
	moderatorStore.Register(addr, "pubkey", 1000)
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT + 50}

	// Delegating 30 leaves 20 available (staked 250 - 30 = 220... wait,
	// use the requirement directly to stay correct if it's ever
	// reconfigured: staked = required+50, delegating 60 leaves
	// required-10, a real 10 CCT shortfall.
	if got := activeModeratorStakeShortfall(addr, 60); got != 10 {
		t.Fatalf("expected a 10 CCT shortfall (required-10 remaining), got %d", got)
	}

	// Delegating only 40 leaves required+10 available - still clears
	// the floor, no shortfall.
	if got := activeModeratorStakeShortfall(addr, 40); got != 0 {
		t.Fatalf("expected 0 shortfall when enough stays available, got %d", got)
	}
}

// TestApplyModeratorRegister_AccountsForAlreadyDelegatedStake confirms
// the apply-side registration check (api/apply.go's
// applyModeratorRegister) - not just the HTTP handler - correctly uses
// available (post-delegation) stake, so a wallet that already delegated
// away enough of its staked CCT can't slip past registration via a
// directly-crafted transaction bypassing the HTTP pre-check.
func TestApplyModeratorRegister_AccountsForAlreadyDelegatedStake(t *testing.T) {
	resetModeratorDelegationTestState()

	const addr = "register-after-delegating"
	walletStore.Get(addr).Reputation = 50
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT}
	cctStore.Mint(addr, cctW(ModeratorRegisterFeeCCT))
	// Delegate away just 1 CCT of the exactly-required stake - available
	// now sits 1 CCT short of the requirement.
	delegationStore.Delegate("some-validator", addr, 1)

	tx := core.Transaction{ID: "test-register-after-delegate", From: addr, Type: core.TxModeratorRegister, Timestamp: 1000}
	applyModeratorRegister(tx)

	if moderatorStore.IsModerator(addr) {
		t.Fatal("expected registration to be blocked - available stake is 1 CCT short of the requirement")
	}
	if got := cctStore.BalanceOf(addr); got != cctW(ModeratorRegisterFeeCCT) {
		t.Fatalf("expected the registration fee to be untouched since registration was blocked, got %s", got)
	}
}

// TestApplyValidatorDelegate_BlocksActiveModeratorBelowFloor is the
// end-to-end regression test: an active moderator attempting to
// delegate enough to drop below their own stake requirement has the
// delegation rejected entirely (no CCT moves, no ledger entry), while
// an ordinary (non-moderator) wallet can delegate that same amount
// freely - the floor only ever applies to moderators.
func TestApplyValidatorDelegate_BlocksActiveModeratorBelowFloor(t *testing.T) {
	resetModeratorDelegationTestState()
	validatorStore.Register("validator-addr", "node-1", "node-pubkey")

	const moderator = "delegating-moderator"
	moderatorStore.Register(moderator, "pubkey", 1000)
	stakingStore.Stakes[moderator] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT + 10}
	cctStore.Mint(moderator, cctW(1000))

	// Delegating 20 would leave only required-10 available - blocked.
	data, _ := json.Marshal(validatorDelegateData{Validator: "validator-addr"})
	tx := core.Transaction{ID: "mod-delegate-blocked", From: moderator, Amount: 20, Type: core.TxValidatorDelegate, Data: string(data), Timestamp: 1000}
	applyValidatorDelegate(tx)

	if got := delegationStore.DelegatedBy("validator-addr", moderator); got != 0 {
		t.Fatalf("expected the over-the-floor delegation to be fully rejected, got %d CCT delegated", got)
	}
	if got := cctStore.BalanceOf(moderator); got != cctW(1000) {
		t.Fatalf("expected the moderator's CCT balance untouched by the rejected delegation, got %s", got)
	}

	// A non-moderator with the identical stake can delegate the same
	// amount freely - the floor only applies to registered moderators.
	const ordinary = "ordinary-delegator"
	stakingStore.Stakes[ordinary] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT + 10}
	cctStore.Mint(ordinary, cctW(1000))
	tx2 := core.Transaction{ID: "ordinary-delegate-ok", From: ordinary, Amount: 20, Type: core.TxValidatorDelegate, Data: string(data), Timestamp: 1000}
	applyValidatorDelegate(tx2)

	if got := delegationStore.DelegatedBy("validator-addr", ordinary); got != 20 {
		t.Fatalf("expected the non-moderator's delegation to succeed, got %d CCT delegated", got)
	}
}

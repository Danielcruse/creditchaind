package api

import (
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// registerEligibleValidatorForLeaderTest gives addr everything
// core.isEligible requires (Active, >= MinValidatorStake CCT staked,
// >= MinValidatorReputationGate reputation, default 1.0 uptime) so it
// actually enters PoSR's strict selection pool - a validator that
// merely calls Register without this is authorized but never
// selectable, which would make currentLeader() skip it regardless of
// this test's intent.
func registerEligibleValidatorForLeaderTest(addr, nodeID, nodePublicKey string) {
	w := walletStore.Get(addr)
	w.Reputation = 50
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": core.MinValidatorStake}
	validatorStore.Register(addr, nodeID, nodePublicKey)
}

// TestVerifyLeaderOrFallback_AcceptsTheActuallyElectedLeader is the
// baseline: the normal, in-turn case must keep working exactly as
// before this fix - a block from the validator core.SelectValidator
// itself would choose is accepted immediately, no timing gate.
func TestVerifyLeaderOrFallback_AcceptsTheActuallyElectedLeader(t *testing.T) {
	resetPoSRTestState()
	blockchain = &core.Blockchain{Blocks: []core.Block{{Index: 0, Hash: "genesis-hash", Timestamp: time.Now().Unix()}}}

	registerEligibleValidatorForLeaderTest("leader-only-addr", "leader-node", "leader-pubkey")

	leader := currentLeader()
	if leader == nil || leader.Wallet.Address != "leader-only-addr" {
		t.Fatalf("test setup issue: expected the single registered validator to be elected leader, got %+v", leader)
	}

	block := core.Block{Validator: "leader-only-addr", Timestamp: time.Now().Unix()}
	if !verifyLeaderOrFallback(block, blockchain.Blocks[0]) {
		t.Fatal("expected a block from the actually-elected leader to be accepted immediately")
	}
}

// TestVerifyLeaderOrFallback_RejectsOutOfTurnBlockProducedImmediately
// is the regression test for audit finding C-02's second half: before
// this fix, block adoption never recomputed the expected leader at
// all, so ANY registered validator could produce arbitrary consecutive
// blocks out of turn. Two eligible validators are registered; whichever
// one core.SelectValidator does NOT choose must be rejected when its
// block arrives immediately after the parent (no real time elapsed).
func TestVerifyLeaderOrFallback_RejectsOutOfTurnBlockProducedImmediately(t *testing.T) {
	resetPoSRTestState()
	blockchain = &core.Blockchain{Blocks: []core.Block{{Index: 0, Hash: "genesis-hash", Timestamp: time.Now().Unix()}}}

	registerEligibleValidatorForLeaderTest("addr-a", "node-a", "pubkey-a")
	registerEligibleValidatorForLeaderTest("addr-b", "node-b", "pubkey-b")

	leader := currentLeader()
	if leader == nil {
		t.Fatal("test setup issue: expected a leader to be elected from two eligible validators")
	}
	outOfTurn := "addr-a"
	if leader.Wallet.Address == "addr-a" {
		outOfTurn = "addr-b"
	}

	block := core.Block{Validator: outOfTurn, Timestamp: time.Now().Unix()}
	if verifyLeaderOrFallback(block, blockchain.Blocks[0]) {
		t.Fatalf("expected a block from %s (not the elected leader %s), arriving with zero elapsed time, to be REJECTED - this is exactly the out-of-turn production the fix closes", outOfTurn, leader.Wallet.Address)
	}
}

// TestVerifyLeaderOrFallback_AcceptsOutOfTurnBlockAfterFallbackWindow
// confirms the missed-block liveness fallback still works: once this
// node's own clock independently agrees the elected leader's window
// has genuinely elapsed (missedRoundsBeforeFallback * consensusTickInterval
// since the parent block), a non-leader validator's block is accepted -
// the same recovery path consensus_loop.go's localAuthorizedValidator
// fallback relies on, now verified by receivers instead of blindly
// trusted.
func TestVerifyLeaderOrFallback_AcceptsOutOfTurnBlockAfterFallbackWindow(t *testing.T) {
	resetPoSRTestState()

	fallbackWindow := int64(missedRoundsBeforeFallback) * int64(consensusTickInterval.Seconds())
	staleParent := core.Block{Index: 0, Hash: "genesis-hash", Timestamp: time.Now().Unix() - fallbackWindow - 5}
	blockchain = &core.Blockchain{Blocks: []core.Block{staleParent}}

	registerEligibleValidatorForLeaderTest("addr-a", "node-a", "pubkey-a")
	registerEligibleValidatorForLeaderTest("addr-b", "node-b", "pubkey-b")

	leader := currentLeader()
	if leader == nil {
		t.Fatal("test setup issue: expected a leader to be elected from two eligible validators")
	}
	outOfTurn := "addr-a"
	if leader.Wallet.Address == "addr-a" {
		outOfTurn = "addr-b"
	}

	block := core.Block{Validator: outOfTurn, Timestamp: time.Now().Unix()}
	if !verifyLeaderOrFallback(block, staleParent) {
		t.Fatalf("expected a block from non-leader %s to be ACCEPTED once the fallback window (%ds) has genuinely elapsed since the parent block", outOfTurn, fallbackWindow)
	}
}

// TestVerifyLeaderOrFallback_IgnoresBlocksOwnClaimedTimestamp is the
// direct test for why the fallback check must use this node's own
// clock rather than trusting block.Timestamp: a producer fully
// controls that field before signing (see core.ComputeBlockHash's
// preimage), so a forged early timestamp claiming the fallback window
// already elapsed must gain an attacker nothing.
func TestVerifyLeaderOrFallback_IgnoresBlocksOwnClaimedTimestamp(t *testing.T) {
	resetPoSRTestState()
	freshParent := core.Block{Index: 0, Hash: "genesis-hash", Timestamp: time.Now().Unix()}
	blockchain = &core.Blockchain{Blocks: []core.Block{freshParent}}

	registerEligibleValidatorForLeaderTest("addr-a", "node-a", "pubkey-a")
	registerEligibleValidatorForLeaderTest("addr-b", "node-b", "pubkey-b")

	leader := currentLeader()
	outOfTurn := "addr-a"
	if leader.Wallet.Address == "addr-a" {
		outOfTurn = "addr-b"
	}

	fallbackWindow := int64(missedRoundsBeforeFallback) * int64(consensusTickInterval.Seconds())
	forged := core.Block{
		Validator: outOfTurn,
		// Claims the fallback window already elapsed, even though the
		// parent (freshParent) was just produced and no real time has
		// actually passed.
		Timestamp: freshParent.Timestamp + fallbackWindow + 10,
	}

	if verifyLeaderOrFallback(forged, freshParent) {
		t.Fatal("expected a forged Timestamp claiming the fallback window elapsed to be REJECTED - verification must use this node's own clock, never the block's self-reported Timestamp")
	}
}

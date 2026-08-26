package api

import (
	"fmt"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func resetGovernanceConvictionTestState() {
	resetApplyTestState()
	resetGovernanceConvictionSweepForTest()
	resetGovernanceConvictionBootstrapForTest()
}

func TestStepGovernanceConviction_GrowsTowardTrustByOnePerEpoch(t *testing.T) {
	w := &core.Wallet{Trust: core.TrustDimensions{GovernanceTrust: 50}}
	w.GovernanceConviction = 0

	stepGovernanceConviction(w)
	if w.GovernanceConviction != 1 {
		t.Fatalf("expected conviction to grow by exactly convictionGrowthPerEpoch (1), got %d", w.GovernanceConviction)
	}
}

func TestStepGovernanceConviction_NeverOvershootsCurrentTrust(t *testing.T) {
	w := &core.Wallet{Trust: core.TrustDimensions{GovernanceTrust: 2}}
	w.GovernanceConviction = 0

	stepGovernanceConviction(w)
	stepGovernanceConviction(w)
	stepGovernanceConviction(w)

	if w.GovernanceConviction != 2 {
		t.Fatalf("expected conviction to stop AT current trust (2), never overshoot, got %d", w.GovernanceConviction)
	}
}

func TestStepGovernanceConviction_DecaysFasterThanItGrows(t *testing.T) {
	w := &core.Wallet{Trust: core.TrustDimensions{GovernanceTrust: 0}}
	w.GovernanceConviction = 100

	stepGovernanceConviction(w)
	if w.GovernanceConviction != 97 {
		t.Fatalf("expected conviction to drop by exactly convictionDecayPerEpoch (3), got %d", w.GovernanceConviction)
	}
}

func TestStepGovernanceConviction_NeverUndershootsCurrentTrustWhileDecaying(t *testing.T) {
	// c=100 decaying by 3 would naturally land at 97, which is BELOW
	// g=99 - the floor must clamp it back up to 99, not let it
	// overshoot past current trust on the way down.
	w := &core.Wallet{Trust: core.TrustDimensions{GovernanceTrust: 99}}
	w.GovernanceConviction = 100

	stepGovernanceConviction(w)

	if w.GovernanceConviction != 99 {
		t.Fatalf("expected conviction to stop AT current trust (99) rather than overshoot downward to 97, got %d", w.GovernanceConviction)
	}
}

func TestStepGovernanceConviction_ConvergesOverManyEpochs(t *testing.T) {
	w := &core.Wallet{Trust: core.TrustDimensions{GovernanceTrust: 100}}
	w.GovernanceConviction = 0

	for i := 0; i < 150; i++ {
		stepGovernanceConviction(w)
	}

	if w.GovernanceConviction != GovernanceConvictionCap {
		t.Fatalf("expected conviction to fully saturate at the cap (%d) after 150 epochs of sustained trust=100, got %d", GovernanceConvictionCap, w.GovernanceConviction)
	}
}

func TestProcessGovernanceConvictionSweep_SkipsNonEpochBlocks(t *testing.T) {
	resetGovernanceConvictionTestState()
	const addr = "conviction-sweep-non-epoch"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Trust = core.TrustDimensions{GovernanceTrust: 80}

	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks - 1, Timestamp: 1000})

	if walletStore.Get(addr).GovernanceConviction != 0 {
		t.Fatalf("expected no conviction movement on a non-epoch block, got %d", walletStore.Get(addr).GovernanceConviction)
	}
}

func TestProcessGovernanceConvictionSweep_SkipsNonActivatedWallets(t *testing.T) {
	resetGovernanceConvictionTestState()
	const addr = "conviction-sweep-not-activated"
	w := walletStore.Get(addr)
	// VotingPower left at 0 - never activated governance.
	w.Trust = core.TrustDimensions{GovernanceTrust: 80}

	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks, Timestamp: 1000})

	if walletStore.Get(addr).GovernanceConviction != 0 {
		t.Fatalf("expected no conviction movement for a non-activated wallet, got %d", walletStore.Get(addr).GovernanceConviction)
	}
}

func TestProcessGovernanceConvictionSweep_StepsActivatedWalletOncePerEpoch(t *testing.T) {
	resetGovernanceConvictionTestState()
	const addr = "conviction-sweep-active"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Trust = core.TrustDimensions{GovernanceTrust: 80}

	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks, Timestamp: 1000})
	if got := walletStore.Get(addr).GovernanceConviction; got != 1 {
		t.Fatalf("expected exactly one growth step within the epoch, got %d", got)
	}

	// A second block in the SAME epoch must not step it again - the
	// batch was already fully drained.
	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks + 1, Timestamp: 1001})
	if got := walletStore.Get(addr).GovernanceConviction; got != 1 {
		t.Fatalf("expected no further movement within the same already-drained epoch, got %d", got)
	}

	// The NEXT epoch boundary steps it again.
	processGovernanceConvictionSweep(core.Block{Index: 2 * convictionSweepEpochBlocks, Timestamp: 2000})
	if got := walletStore.Get(addr).GovernanceConviction; got != 2 {
		t.Fatalf("expected a second growth step at the next epoch boundary, got %d", got)
	}
}

func makeConvictionEligibleWallets(n int, prefix string) []string {
	addrs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		addr := fmt.Sprintf("%s-%04d", prefix, i)
		w := walletStore.Get(addr)
		w.VotingPower = 1
		w.Trust = core.TrustDimensions{GovernanceTrust: 80}
		addrs = append(addrs, addr)
	}
	return addrs
}

func countSteppedWallets(addrs []string) int {
	n := 0
	for _, addr := range addrs {
		if walletStore.Get(addr).GovernanceConviction > 0 {
			n++
		}
	}
	return n
}

// TestGovernanceConvictionSweep_PersistsAcrossRestart mirrors
// TestReputationMiningSweep_PersistsAcrossRestart exactly - simulates a
// restart mid-epoch (snapshot, wipe in-memory state, restore) and
// confirms the sweep resumes the batch cursor instead of re-stepping
// wallets already processed earlier in the same epoch (which would be
// a real double-step bug, not just a missed optimization, for the same
// reason the mining sweep's own snapshot is load-bearing).
func TestGovernanceConvictionSweep_PersistsAcrossRestart(t *testing.T) {
	resetGovernanceConvictionTestState()

	total := convictionBatchSize + 50
	addrs := makeConvictionEligibleWallets(total, "conviction-restart-wallet")

	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks, Timestamp: 1000})
	if got := countSteppedWallets(addrs); got != convictionBatchSize {
		t.Fatalf("test setup issue: expected %d stepped before simulating a restart, got %d", convictionBatchSize, got)
	}

	snap := snapshotGovernanceConvictionSweep()
	resetGovernanceConvictionSweepForTest()
	restoreGovernanceConvictionSweep(snap)

	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks + 1, Timestamp: 1001})

	if got := countSteppedWallets(addrs); got != total {
		t.Fatalf("expected all %d wallets stepped after resuming post-restart, got %d", total, got)
	}

	// Re-running the same next block again must not double-step anyone
	// - the pending list is now empty. Every wallet stepped exactly
	// once should sit at conviction==1, never 2.
	for _, addr := range addrs {
		if got := walletStore.Get(addr).GovernanceConviction; got != 1 {
			t.Fatalf("expected wallet %s stepped exactly once (conviction=1), got %d - this would indicate a double-step from resuming without the persisted cursor", addr, got)
		}
	}
	processGovernanceConvictionSweep(core.Block{Index: convictionSweepEpochBlocks + 2, Timestamp: 1002})
	for _, addr := range addrs {
		if got := walletStore.Get(addr).GovernanceConviction; got != 1 {
			t.Fatalf("expected no double-step from reprocessing an already-fully-drained epoch, wallet %s got %d", addr, got)
		}
	}
}

// TestGovernanceConvictionBootstrap_RunsOnceAndGrandfathersCurrentTrust
// mirrors legacy_stake_migration's one-time-sweep pattern: on its
// first-ever pass, every activated wallet's GovernanceConviction is
// set DIRECTLY to its current GovernanceTrust (not grown one step at a
// time) - grandfathering already-engaged wallets in rather than
// starting everyone at 0 the instant this feature ships.
func TestGovernanceConvictionBootstrap_RunsOnceAndGrandfathersCurrentTrust(t *testing.T) {
	resetGovernanceConvictionTestState()

	const addr = "conviction-bootstrap-wallet"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Trust = core.TrustDimensions{GovernanceTrust: 73}

	processGovernanceConvictionBootstrap(core.Block{Index: 1, Timestamp: 1000})

	if got := walletStore.Get(addr).GovernanceConviction; got != 73 {
		t.Fatalf("expected the bootstrap to set conviction DIRECTLY to current trust (73) on its first pass, got %d", got)
	}
}

func TestGovernanceConvictionBootstrap_IsPermanentlyIdempotent(t *testing.T) {
	resetGovernanceConvictionTestState()

	const addr = "conviction-bootstrap-idempotent"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Trust = core.TrustDimensions{GovernanceTrust: 60}

	processGovernanceConvictionBootstrap(core.Block{Index: 1, Timestamp: 1000})
	if got := walletStore.Get(addr).GovernanceConviction; got != 60 {
		t.Fatalf("setup issue: expected conviction 60 after first bootstrap pass, got %d", got)
	}

	// Trust changes AFTER the one-time bootstrap has already run - a
	// second call must be a permanent no-op, never re-grandfathering.
	w.Trust = core.TrustDimensions{GovernanceTrust: 10}
	processGovernanceConvictionBootstrap(core.Block{Index: 2, Timestamp: 1001})

	if got := walletStore.Get(addr).GovernanceConviction; got != 60 {
		t.Fatalf("expected the one-time bootstrap to be a permanent no-op after its first pass, got conviction changed to %d", got)
	}
}

func TestGovernanceConvictionBootstrap_PersistsAcrossRestart(t *testing.T) {
	resetGovernanceConvictionTestState()

	total := convictionBootstrapBatchSize + 50
	addrs := makeConvictionEligibleWallets(total, "conviction-bootstrap-restart")

	processGovernanceConvictionBootstrap(core.Block{Index: 1, Timestamp: 1000})
	firstPassCount := 0
	for _, addr := range addrs {
		if walletStore.Get(addr).GovernanceConviction > 0 {
			firstPassCount++
		}
	}
	if firstPassCount != convictionBootstrapBatchSize {
		t.Fatalf("test setup issue: expected %d grandfathered before simulating a restart, got %d", convictionBootstrapBatchSize, firstPassCount)
	}

	snap := snapshotGovernanceConvictionBootstrap()
	resetGovernanceConvictionBootstrapForTest()
	restoreGovernanceConvictionBootstrap(snap)

	processGovernanceConvictionBootstrap(core.Block{Index: 2, Timestamp: 1001})

	for _, addr := range addrs {
		if got := walletStore.Get(addr).GovernanceConviction; got != 80 {
			t.Fatalf("expected wallet %s grandfathered to its trust value (80) after resuming post-restart, got %d", addr, got)
		}
	}
}

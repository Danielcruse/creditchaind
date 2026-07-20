package api

import (
	"fmt"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// makeMiningEligibleWallets registers n wallets with LastActivityAt
// set and returns their sorted addresses - a real Mining Power score
// isn't needed here, only that each wallet mints a nonzero amount, so
// Reputation is set high enough to guarantee that.
func makeMiningEligibleWallets(n int, prefix string) []string {
	addrs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		addr := fmt.Sprintf("%s-%04d", prefix, i)
		w := walletStore.Get(addr)
		w.Reputation = 100
		w.LastActivityAt = 500
		addrs = append(addrs, addr)
	}
	return addrs
}

// countMintedWallets returns how many of addrs currently have a
// nonzero REPT balance.
func countMintedWallets(addrs []string) int {
	n := 0
	for _, addr := range addrs {
		if reptStore.BalanceOf(addr) > 0 {
			n++
		}
	}
	return n
}

// TestProcessReputationMining_SpreadsLargeWalletSetAcrossMultipleBlocks
// is the core regression test for the O(all wallets)-in-one-block fix:
// with more eligible wallets than miningBatchSize, the first block of
// a new epoch must mint to only a bounded batch, not everyone at once
// - the remainder finishes on subsequent blocks within the same epoch.
func TestProcessReputationMining_SpreadsLargeWalletSetAcrossMultipleBlocks(t *testing.T) {
	resetApplyTestState()

	total := miningBatchSize + 50 // guarantees at least 2 batches are needed
	addrs := makeMiningEligibleWallets(total, "mining-batch-wallet")

	// First block of the new epoch - should mint to exactly
	// miningBatchSize wallets, not all of them.
	processReputationMining(core.Block{Index: miningEpochBlocks, Timestamp: 1000})

	if got := countMintedWallets(addrs); got != miningBatchSize {
		t.Fatalf("expected exactly %d wallets minted on the first block of the epoch, got %d", miningBatchSize, got)
	}

	// A later block still within the same epoch should drain the rest.
	processReputationMining(core.Block{Index: miningEpochBlocks + 1, Timestamp: 1001})

	if got := countMintedWallets(addrs); got != total {
		t.Fatalf("expected all %d wallets minted after a second block drained the remainder, got %d", total, got)
	}

	// A third block, with nothing left pending, must be a cheap no-op
	// - re-verify no wallet is minted a second time (would silently
	// double a wallet's reward for the same epoch).
	balancesBefore := make(map[string]uint64, len(addrs))
	for _, addr := range addrs {
		balancesBefore[addr] = reptStore.BalanceOf(addr)
	}
	processReputationMining(core.Block{Index: miningEpochBlocks + 2, Timestamp: 1002})
	for _, addr := range addrs {
		if reptStore.BalanceOf(addr) != balancesBefore[addr] {
			t.Fatalf("expected no further minting once the epoch's pending list is drained, but %s's balance changed", addr)
		}
	}
}

// TestProcessReputationMining_NewEpochResnapshotsAfterPriorEpochDrained
// confirms a second epoch boundary correctly starts a fresh sweep
// rather than staying stuck on an empty pending list forever.
func TestProcessReputationMining_NewEpochResnapshotsAfterPriorEpochDrained(t *testing.T) {
	resetApplyTestState()

	const addr = "mining-second-epoch-wallet"
	w := walletStore.Get(addr)
	w.Reputation = 100
	w.LastActivityAt = 500

	processReputationMining(core.Block{Index: miningEpochBlocks, Timestamp: 1000})
	firstEpochBalance := reptStore.BalanceOf(addr)
	if firstEpochBalance == 0 {
		t.Fatalf("test setup issue: expected a nonzero mint in the first epoch")
	}

	// Still epoch 1 - must not mint again (already drained).
	processReputationMining(core.Block{Index: miningEpochBlocks + 5, Timestamp: 1005})
	if got := reptStore.BalanceOf(addr); got != firstEpochBalance {
		t.Fatalf("expected balance unchanged mid-epoch after draining, got %d (was %d)", got, firstEpochBalance)
	}

	// A genuinely new epoch boundary - should mint again.
	processReputationMining(core.Block{Index: 2 * miningEpochBlocks, Timestamp: 2000})
	if got := reptStore.BalanceOf(addr); got <= firstEpochBalance {
		t.Fatalf("expected a second mint once a new epoch boundary is reached, balance stayed at %d", got)
	}
}

// TestReputationMiningSweep_PersistsAcrossRestart is the correctness
// test for why this state MUST be persisted (see reputation_mining.go's
// doc comment on reputationMiningSweepSnapshot): simulate a restart
// mid-epoch (snapshot then reset the in-memory state, as a real
// process restart would before LoadState runs) and confirm restoring
// the snapshot resumes the batch cursor instead of re-processing
// already-minted wallets.
func TestReputationMiningSweep_PersistsAcrossRestart(t *testing.T) {
	resetApplyTestState()

	total := miningBatchSize + 50
	addrs := makeMiningEligibleWallets(total, "mining-restart-wallet")

	processReputationMining(core.Block{Index: miningEpochBlocks, Timestamp: 1000})
	if got := countMintedWallets(addrs); got != miningBatchSize {
		t.Fatalf("test setup issue: expected %d minted before simulating a restart, got %d", miningBatchSize, got)
	}

	// Simulate a restart: snapshot the current cursor, wipe in-memory
	// state (as a fresh process would start with), then restore -
	// exactly the SaveState/process-restart/LoadState sequence.
	snap := snapshotReputationMiningSweep()
	resetReputationMiningSweepForTest()
	restoreReputationMiningSweep(snap)

	// Same epoch, next block - must continue draining the REMAINING
	// wallets only, not re-snapshot and re-mint the first batch again.
	processReputationMining(core.Block{Index: miningEpochBlocks + 1, Timestamp: 1001})

	if got := countMintedWallets(addrs); got != total {
		t.Fatalf("expected all %d wallets minted after resuming post-restart, got %d", total, got)
	}

	// Re-run the same next block again (as if it were somehow
	// reprocessed) - the pending list is now empty, so no wallet
	// should gain a second mint. This is the actual double-mint
	// regression this test guards: without persistence, resuming
	// mid-epoch would re-snapshot ALL wallets (including the first
	// batch already minted before the simulated restart) and mint to
	// them again.
	balancesAfterFirstDrain := make(map[string]uint64, len(addrs))
	for _, addr := range addrs {
		balancesAfterFirstDrain[addr] = reptStore.BalanceOf(addr)
	}
	processReputationMining(core.Block{Index: miningEpochBlocks + 2, Timestamp: 1002})
	for _, addr := range addrs {
		if reptStore.BalanceOf(addr) != balancesAfterFirstDrain[addr] {
			t.Fatalf("expected %s to be minted exactly once this epoch - a restart without persistence would double-mint the first batch", addr)
		}
	}
}

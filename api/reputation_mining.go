package api

import (
	"sort"
	"sync"

	"github.com/creditchain/creditchaind/core"
)

const (
	// miningEpochBlocks gates processReputationMining to run roughly
	// once/day, not every block - see the function doc comment for
	// why. Judgment call: consensusTickInterval (api/consensus_loop.go)
	// is ~8s/round, so 10800 blocks is a rough (not exact - round
	// timing varies) daily cadence.
	miningEpochBlocks = 10800

	// baseEpochMiningEmission is the REPT minted per epoch to a wallet
	// at MiningPower==100 (minted = baseEpochMiningEmission*score/100).
	// Judgment call, sized so continuous mining meaningfully outpaces
	// the reduced one-time tier rewards (core/reputation_reward.go)
	// within days, per the product decision that mining becomes the
	// dominant REPT source going forward.
	baseEpochMiningEmission = 20

	// miningBatchSize bounds how many wallets processReputationMining
	// mints to in any single block, regardless of total wallet count -
	// see the function doc comment for why this replaced the old
	// do-everyone-in-one-block design. Judgment call, not derived from
	// a specified model - large enough that a small/medium network
	// finishes its whole epoch in the very first block after the
	// boundary (matching the old design's observable behavior exactly
	// at today's scale), small enough to keep any single block's added
	// mining cost bounded as the wallet count grows over years.
	miningBatchSize = 250
)

// reputationMiningSweep holds the in-progress state for the CURRENT
// epoch's wallet sweep - which epoch it belongs to, and which
// already-collected, already-sorted addresses still haven't been
// minted to yet this epoch. See processReputationMining's doc comment
// for why this exists instead of a single all-at-once pass.
var reputationMiningSweep = struct {
	mu           sync.Mutex
	initialized  bool
	epochIndex   uint64
	pendingAddrs []string
}{}

// processReputationMining mints REPT ("Reputation Credits") to every
// wallet in good standing, proportional to its Mining Power score
// (miningPowerScore, api/mining_power.go) - the continuous-mining half
// of the Reputation Credit spec, alongside the existing one-time
// tier-crossing reward (core/reputation_reward.go).
//
// Gated to a new "epoch" roughly once/day (keyed purely off
// block.Index, never wall-clock): the first block of a new epoch
// takes a fresh, sorted snapshot of every eligible wallet address and
// processes the first miningBatchSize of them; subsequent blocks
// within the same epoch drain the remainder in the same bounded
// batches, one bite per block, until the snapshot is exhausted (after
// which this becomes a cheap no-op for the rest of the epoch). This
// replaced an earlier design that minted to every eligible wallet in
// a single block at the exact epoch boundary - correct, but an
// unbounded per-block cost that would only grow as the wallet count
// grows over the network's lifetime (see audit/KNOWN_GAPS.md's
// now-resolved "O(all wallets) sweep" entry). Batching still leaves
// the FULL SWEEP itself O(all wallets) once per epoch in aggregate
// cost - this bounds the worst case for any single block, which was
// the actual problem, not the total epoch-wide work.
//
// Determinism is preserved: every node computes the identical
// block.Index-derived epoch number and the identical sorted address
// snapshot from the same already-applied, consensus-agreed
// walletStore state, so every node drains the same batches in the
// same order on the same blocks - this never depends on any node's
// own local timing or state.
//
// "Good standing" is deliberately just LastActivityAt != 0 (has ever
// signed a transaction), NOT !IsDormant() - core.DormancyPeriodSeconds
// is a demo-scale stand-in for what's meant to be a 50-YEAR reclaim
// window (see core/dormancy.go's own doc comment), so gating mining
// eligibility on it would exclude nearly every wallet in this
// environment and make continuous mining non-functional in practice.
// LastActivityAt != 0 still gets the intended benefit (excluding
// never-touched synthetic pool/escrow addresses) without conflating a
// long-horizon reclaim rule with day-to-day mining eligibility.
func processReputationMining(block core.Block) {
	currentEpoch := block.Index / miningEpochBlocks
	if currentEpoch == 0 {
		// Genesis epoch - not a real epoch boundary yet (generalizes
		// the old design's explicit block.Index==0 exclusion to the
		// whole [0, miningEpochBlocks) window).
		return
	}

	reputationMiningSweep.mu.Lock()
	if !reputationMiningSweep.initialized || reputationMiningSweep.epochIndex != currentEpoch {
		reputationMiningSweep.initialized = true
		reputationMiningSweep.epochIndex = currentEpoch
		reputationMiningSweep.pendingAddrs = collectMiningEligibleAddrs()
	}
	if len(reputationMiningSweep.pendingAddrs) == 0 {
		reputationMiningSweep.mu.Unlock()
		return // this epoch's wallets are already fully processed
	}
	n := miningBatchSize
	if n > len(reputationMiningSweep.pendingAddrs) {
		n = len(reputationMiningSweep.pendingAddrs)
	}
	batch := reputationMiningSweep.pendingAddrs[:n]
	reputationMiningSweep.pendingAddrs = reputationMiningSweep.pendingAddrs[n:]
	reputationMiningSweep.mu.Unlock()

	for _, addr := range batch {
		amount := baseEpochMiningEmission * miningPowerScore(addr, block.Timestamp) / 100
		if amount > 0 {
			reptStore.Mint(addr, amount)
		}
	}
}

// collectMiningEligibleAddrs snapshots every currently-known wallet
// address with LastActivityAt != 0, sorted for the same
// cross-node-determinism reason processDormantWallets/
// processLoanDefaults already sort their own iteration order.
func collectMiningEligibleAddrs() []string {
	var addrs []string
	for addr, w := range walletStore.Wallets {
		if w.LastActivityAt == 0 {
			continue
		}
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	return addrs
}

// resetReputationMiningSweepForTest clears the in-progress epoch-sweep
// state - test-only, mirrors this package's other reset-hooks for
// package-level singletons (see resetApplyTestState).
func resetReputationMiningSweepForTest() {
	reputationMiningSweep.mu.Lock()
	defer reputationMiningSweep.mu.Unlock()
	reputationMiningSweep.initialized = false
	reputationMiningSweep.epochIndex = 0
	reputationMiningSweep.pendingAddrs = nil
}

// reputationMiningSweepSnapshot/snapshotReputationMiningSweep/
// restoreReputationMiningSweep persist the in-progress batch cursor
// across a restart - NOT optional here, unlike most of this
// codebase's other dedup snapshots: without it, a node that restarts
// mid-epoch (after already minting to some of this epoch's wallets)
// would see initialized==false on the next call, re-snapshot the
// FULL wallet set again, and re-mint the wallets it already processed
// earlier in the same epoch - a real double-mint bug, not just a
// missed-optimization.
type reputationMiningSweepSnapshot struct {
	Initialized  bool     `json:"initialized"`
	EpochIndex   uint64   `json:"epochIndex"`
	PendingAddrs []string `json:"pendingAddrs"`
}

func snapshotReputationMiningSweep() reputationMiningSweepSnapshot {
	reputationMiningSweep.mu.Lock()
	defer reputationMiningSweep.mu.Unlock()

	addrs := make([]string, len(reputationMiningSweep.pendingAddrs))
	copy(addrs, reputationMiningSweep.pendingAddrs)

	return reputationMiningSweepSnapshot{
		Initialized:  reputationMiningSweep.initialized,
		EpochIndex:   reputationMiningSweep.epochIndex,
		PendingAddrs: addrs,
	}
}

func restoreReputationMiningSweep(snap reputationMiningSweepSnapshot) {
	reputationMiningSweep.mu.Lock()
	defer reputationMiningSweep.mu.Unlock()

	reputationMiningSweep.initialized = snap.Initialized
	reputationMiningSweep.epochIndex = snap.EpochIndex
	reputationMiningSweep.pendingAddrs = snap.PendingAddrs
}

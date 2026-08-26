package api

import (
	"sort"
	"sync"

	"github.com/creditchain/creditchaind/core"
)

const (
	// convictionSweepEpochBlocks reuses reputation-mining's own daily
	// cadence (api/reputation_mining.go's miningEpochBlocks) - no
	// stated reason to pick a different one.
	convictionSweepEpochBlocks = 10800

	// convictionBatchSize reuses miningBatchSize's value/reasoning
	// verbatim - same defensive-from-day-one batching discipline.
	convictionBatchSize = 250

	// convictionGrowthPerEpoch/convictionDecayPerEpoch are the
	// asymmetric per-epoch step sizes governanceConvictionSweep chases
	// GovernanceTrust with - see Wallet.GovernanceConviction's doc
	// comment. Judgment calls, not derived from a specified model:
	// growth=1 means ~100 days of SUSTAINED high GovernanceTrust to
	// fully saturate (genuinely "built over time," not just "old
	// enough"); decay=3 (3x faster) means a wallet that stops earning
	// GovernanceTrust unwinds in ~33 days, bounding the abuse window of
	// "build conviction, then coast on high vote weight while
	// misbehaving" to well under the time it took to build - mirrors
	// this codebase's existing asymmetric-signal convention
	// (GovernanceTrustDeltaProposerPassed=5 vs ...VoterPassed=1,
	// api/apply.go).
	convictionGrowthPerEpoch = 1
	convictionDecayPerEpoch  = 3

	// GovernanceConvictionCap mirrors the 1-100 scale every trust
	// dimension already uses (core/trust.go) - deliberately the same
	// range as GovernanceTrust so the two are directly comparable
	// without a unit conversion.
	GovernanceConvictionCap = 100
)

// governanceConvictionSweep holds the in-progress state for the
// CURRENT epoch's recurring conviction sweep - identical shape to
// reputationMiningSweep (api/reputation_mining.go), same reasoning for
// why a single all-at-once pass isn't used.
var governanceConvictionSweep = struct {
	mu           sync.Mutex
	initialized  bool
	epochIndex   uint64
	pendingAddrs []string
}{}

// processGovernanceConvictionSweep chases every activated-governance
// wallet's GovernanceConviction toward its current GovernanceTrust,
// one asymmetric step per epoch - see the constants above for the
// growth/decay formula and reasoning. Eligibility is
// wallet.VotingPower > 0 (activated governance), narrower than
// reputation mining's LastActivityAt != 0 gate: conviction is only
// ever consumed by votingWeightForNodeAdmission
// (api/governance_weight.go), so sweeping a non-activated wallet would
// be pure waste. Same block.Index-derived epoch/batch/determinism
// discipline as processReputationMining - see that function's own doc
// comment for the full reasoning, not repeated here.
func processGovernanceConvictionSweep(block core.Block) {
	currentEpoch := block.Index / convictionSweepEpochBlocks
	if currentEpoch == 0 {
		return
	}

	governanceConvictionSweep.mu.Lock()
	if !governanceConvictionSweep.initialized || governanceConvictionSweep.epochIndex != currentEpoch {
		governanceConvictionSweep.initialized = true
		governanceConvictionSweep.epochIndex = currentEpoch
		governanceConvictionSweep.pendingAddrs = collectConvictionEligibleAddrs()
	}
	if len(governanceConvictionSweep.pendingAddrs) == 0 {
		governanceConvictionSweep.mu.Unlock()
		return
	}
	n := convictionBatchSize
	if n > len(governanceConvictionSweep.pendingAddrs) {
		n = len(governanceConvictionSweep.pendingAddrs)
	}
	batch := governanceConvictionSweep.pendingAddrs[:n]
	governanceConvictionSweep.pendingAddrs = governanceConvictionSweep.pendingAddrs[n:]
	governanceConvictionSweep.mu.Unlock()

	for _, addr := range batch {
		stepGovernanceConviction(walletStore.Get(addr))
	}
}

// stepGovernanceConviction applies exactly one asymmetric growth/decay
// step, converging toward current GovernanceTrust - both directions
// are the same update (min toward g when below, max toward g when
// above), so there's no separate "trust just dropped" event to detect.
func stepGovernanceConviction(w *core.Wallet) {
	g := core.TrustDimensionValue(w, core.DimensionGovernance)
	c := w.GovernanceConviction

	if g >= c {
		c += convictionGrowthPerEpoch
		if c > g {
			c = g
		}
		if c > GovernanceConvictionCap {
			c = GovernanceConvictionCap
		}
	} else {
		if c > convictionDecayPerEpoch {
			c -= convictionDecayPerEpoch
		} else {
			c = 0
		}
		if c < g {
			c = g
		}
	}
	w.GovernanceConviction = c
}

// collectConvictionEligibleAddrs snapshots every wallet with activated
// governance (VotingPower > 0) at sweep-start, sorted for the same
// cross-node-determinism reason every other sweep in this codebase
// sorts its iteration order.
func collectConvictionEligibleAddrs() []string {
	var addrs []string
	for addr, w := range walletStore.Wallets {
		if w.VotingPower == 0 {
			continue
		}
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	return addrs
}

// resetGovernanceConvictionSweepForTest mirrors
// resetReputationMiningSweepForTest - test-only.
func resetGovernanceConvictionSweepForTest() {
	governanceConvictionSweep.mu.Lock()
	defer governanceConvictionSweep.mu.Unlock()
	governanceConvictionSweep.initialized = false
	governanceConvictionSweep.epochIndex = 0
	governanceConvictionSweep.pendingAddrs = nil
}

// governanceConvictionSweepSnapshot persists the in-progress batch
// cursor across a restart - same non-optional reasoning as
// reputationMiningSweepSnapshot: without it, a restart mid-epoch would
// re-snapshot the full eligible set and re-apply a growth/decay step to
// wallets already stepped earlier that epoch, double-counting movement
// within a single epoch.
type governanceConvictionSweepSnapshot struct {
	Initialized  bool     `json:"initialized"`
	EpochIndex   uint64   `json:"epochIndex"`
	PendingAddrs []string `json:"pendingAddrs"`
}

func snapshotGovernanceConvictionSweep() governanceConvictionSweepSnapshot {
	governanceConvictionSweep.mu.Lock()
	defer governanceConvictionSweep.mu.Unlock()

	addrs := make([]string, len(governanceConvictionSweep.pendingAddrs))
	copy(addrs, governanceConvictionSweep.pendingAddrs)

	return governanceConvictionSweepSnapshot{
		Initialized:  governanceConvictionSweep.initialized,
		EpochIndex:   governanceConvictionSweep.epochIndex,
		PendingAddrs: addrs,
	}
}

func restoreGovernanceConvictionSweep(snap governanceConvictionSweepSnapshot) {
	governanceConvictionSweep.mu.Lock()
	defer governanceConvictionSweep.mu.Unlock()

	governanceConvictionSweep.initialized = snap.Initialized
	governanceConvictionSweep.epochIndex = snap.EpochIndex
	governanceConvictionSweep.pendingAddrs = snap.PendingAddrs
}

// --- One-time bootstrap: grandfather already-engaged wallets in at
// their current GovernanceTrust, rather than starting everyone at 0
// the instant this feature ships. Mirrors
// api/legacy_stake_migration.go's legacyStakeMigrationSweep shape
// exactly (batched, persisted, ONE-TIME not recurring) - see that
// file's own doc comment for the full batched/one-time reasoning, not
// repeated here. A fixed-block-index design was considered and
// rejected in favor of mirroring this already-proven pattern (see the
// node-admission plan's own "Bootstrapping existing wallets" section
// for why).

const convictionBootstrapBatchSize = 250

var governanceConvictionBootstrap = struct {
	mu           sync.Mutex
	initialized  bool
	done         bool
	pendingAddrs []string
}{}

// processGovernanceConvictionBootstrap runs exactly once (ever, per
// node lifetime, then becomes a permanent no-op) - see
// governanceConvictionBootstrap.done. Sets each already-activated
// wallet's GovernanceConviction directly to its current
// GovernanceTrust, treating pre-feature governance history as already-
// proven sustained trust. A minor, self-resolving, non-bug race is
// possible if the recurring sweep (above) reaches a wallet before this
// bootstrap does: the recurring sweep would grow that wallet's
// conviction from 0 by one normal step first, then this bootstrap
// overwrites it directly to the correct value on its own next pass - a
// wallet might show understated conviction for one or two epochs,
// never a wrong end state.
func processGovernanceConvictionBootstrap(block core.Block) {
	governanceConvictionBootstrap.mu.Lock()
	if governanceConvictionBootstrap.done {
		governanceConvictionBootstrap.mu.Unlock()
		return
	}
	if !governanceConvictionBootstrap.initialized {
		governanceConvictionBootstrap.initialized = true
		governanceConvictionBootstrap.pendingAddrs = collectConvictionEligibleAddrs()
	}
	if len(governanceConvictionBootstrap.pendingAddrs) == 0 {
		governanceConvictionBootstrap.done = true
		governanceConvictionBootstrap.mu.Unlock()
		return
	}
	n := convictionBootstrapBatchSize
	if n > len(governanceConvictionBootstrap.pendingAddrs) {
		n = len(governanceConvictionBootstrap.pendingAddrs)
	}
	batch := governanceConvictionBootstrap.pendingAddrs[:n]
	governanceConvictionBootstrap.pendingAddrs = governanceConvictionBootstrap.pendingAddrs[n:]
	if len(governanceConvictionBootstrap.pendingAddrs) == 0 {
		governanceConvictionBootstrap.done = true
	}
	governanceConvictionBootstrap.mu.Unlock()

	for _, addr := range batch {
		w := walletStore.Get(addr)
		w.GovernanceConviction = core.TrustDimensionValue(w, core.DimensionGovernance)
	}
}

func resetGovernanceConvictionBootstrapForTest() {
	governanceConvictionBootstrap.mu.Lock()
	defer governanceConvictionBootstrap.mu.Unlock()
	governanceConvictionBootstrap.initialized = false
	governanceConvictionBootstrap.done = false
	governanceConvictionBootstrap.pendingAddrs = nil
}

type governanceConvictionBootstrapSnapshot struct {
	Initialized  bool     `json:"initialized"`
	Done         bool     `json:"done"`
	PendingAddrs []string `json:"pendingAddrs"`
}

func snapshotGovernanceConvictionBootstrap() governanceConvictionBootstrapSnapshot {
	governanceConvictionBootstrap.mu.Lock()
	defer governanceConvictionBootstrap.mu.Unlock()

	addrs := make([]string, len(governanceConvictionBootstrap.pendingAddrs))
	copy(addrs, governanceConvictionBootstrap.pendingAddrs)

	return governanceConvictionBootstrapSnapshot{
		Initialized:  governanceConvictionBootstrap.initialized,
		Done:         governanceConvictionBootstrap.done,
		PendingAddrs: addrs,
	}
}

func restoreGovernanceConvictionBootstrap(snap governanceConvictionBootstrapSnapshot) {
	governanceConvictionBootstrap.mu.Lock()
	defer governanceConvictionBootstrap.mu.Unlock()

	governanceConvictionBootstrap.initialized = snap.Initialized
	governanceConvictionBootstrap.done = snap.Done
	governanceConvictionBootstrap.pendingAddrs = snap.PendingAddrs
}

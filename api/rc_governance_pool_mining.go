package api

import (
	"sync"

	"github.com/creditchain/creditchaind/core"
)

// governanceRCPoolAddress is the synthetic sink the governance RC pool
// accrues into - the same "POOL:"+name synthetic-address convention
// already used by poolAddress(symbol) (api/apply.go) and
// tokenCreationFeeAddress (api/token_registry.go). Nobody holds the
// private key for it (same as every other synthetic pool address in
// this codebase) - its balance is transparently visible via
// /token/balance?symbol=REPT but has no build-out spend/withdrawal
// mechanism in this phase, deliberately: this pool is read-only/
// observable infrastructure for now (see GET /rc/leaderboard's sibling
// endpoint for per-wallet RC; this pool's own balance is readable the
// same way any REPT balance is). A future governance-authorized spend
// proposal is the natural next step, not attempted here.
func governanceRCPoolAddress() string {
	return "POOL:GOVERNANCE_RC"
}

// governanceRCPoolEmissionBps is a judgment call, tunable - deliberately
// small relative to baseEpochMiningEmission's per-wallet continuous
// mining, since this pool's own emission scales off the much larger
// governanceTotalWeight() aggregate rather than one wallet's own score.
// 50 bps = 0.50% of aggregate governance weight, minted once per
// mining epoch.
const governanceRCPoolEmissionBps = 50

var governanceRCPoolMintState = struct {
	mu          sync.Mutex
	initialized bool
	lastEpoch   uint64
}{}

// processGovernanceRCPoolMining credits governanceRCPoolAddress() once
// per mining epoch (the same miningEpochBlocks cadence
// processReputationMining already uses), scaled by
// governanceTotalWeight() - the existing "aggregate reputation signal
// across active governance participants" (api/governance_weight.go).
// Unlike processReputationMining, this credits exactly one address, so
// it needs no batching/sweep-cursor machinery - a plain
// once-per-epoch guard is enough, O(1) per block.
func processGovernanceRCPoolMining(block core.Block) {
	currentEpoch := block.Index / miningEpochBlocks
	if currentEpoch == 0 {
		return
	}

	governanceRCPoolMintState.mu.Lock()
	alreadyMinted := governanceRCPoolMintState.initialized && governanceRCPoolMintState.lastEpoch == currentEpoch
	if !alreadyMinted {
		governanceRCPoolMintState.initialized = true
		governanceRCPoolMintState.lastEpoch = currentEpoch
	}
	governanceRCPoolMintState.mu.Unlock()
	if alreadyMinted {
		return
	}

	amount := governanceTotalWeight() * governanceRCPoolEmissionBps / 10000
	if amount > 0 {
		reptStore.Mint(governanceRCPoolAddress(), amount)
	}
}

// governanceRCPoolMintSnapshot is the restart-safe persistence for the
// once-per-epoch guard above - same rationale as
// reputationMiningSweepSnapshot (api/reputation_mining.go): without
// this, a restart mid-epoch would let the next block re-mint the same
// epoch's pool credit a second time.
type governanceRCPoolMintSnapshot struct {
	Initialized bool   `json:"initialized"`
	LastEpoch   uint64 `json:"lastEpoch"`
}

func snapshotGovernanceRCPoolMint() governanceRCPoolMintSnapshot {
	governanceRCPoolMintState.mu.Lock()
	defer governanceRCPoolMintState.mu.Unlock()
	return governanceRCPoolMintSnapshot{
		Initialized: governanceRCPoolMintState.initialized,
		LastEpoch:   governanceRCPoolMintState.lastEpoch,
	}
}

func restoreGovernanceRCPoolMint(s governanceRCPoolMintSnapshot) {
	governanceRCPoolMintState.mu.Lock()
	defer governanceRCPoolMintState.mu.Unlock()
	governanceRCPoolMintState.initialized = s.Initialized
	governanceRCPoolMintState.lastEpoch = s.LastEpoch
}

func resetGovernanceRCPoolMintForTest() {
	governanceRCPoolMintState.mu.Lock()
	defer governanceRCPoolMintState.mu.Unlock()
	governanceRCPoolMintState.initialized = false
	governanceRCPoolMintState.lastEpoch = 0
}

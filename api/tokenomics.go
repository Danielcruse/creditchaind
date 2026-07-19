package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// MaxTotalCCTSupply is PoSR's lifetime CCT supply cap - every CCT mint
// path (block rewards, staking rewards, the admin faucet, and the
// one-time genesis pool allocation) is bounded by it via
// mintCCTCapped. Once total supply reaches this ceiling, block
// rewards correctly mint 0 (same "reward asymptotes to zero at the
// cap" behavior real fixed-supply chains use) - transaction fees
// (already fully wired, see applyBlockReward) become validators' sole
// income at that point, exactly matching PoSR's own "controlled
// inflation" framing.
const MaxTotalCCTSupply = 5_000_000_000

// mintCCTCapped mints up to amount of CCT to address, clamped so
// tokenStore's TotalSupply never exceeds MaxTotalCCTSupply. Returns
// the amount actually minted (may be less than requested, or zero
// once at the cap) - every CCT-minting call site in this package must
// route through this rather than calling tokenStore.Mint directly.
func mintCCTCapped(address string, amount uint64) uint64 {
	if amount == 0 {
		return 0
	}
	supply := tokenStore.Token.TotalSupply
	if supply >= MaxTotalCCTSupply {
		return 0
	}
	headroom := MaxTotalCCTSupply - supply
	if amount > headroom {
		amount = headroom
	}
	tokenStore.Mint(address, amount)
	return amount
}

// blockRewardEra is one (startYear, rewardCCT) step in PoSR's
// controlled-inflation decay curve.
type blockRewardEra struct {
	startYear int64
	rewardCCT uint64
}

// blockRewardSchedule implements PoSR's decaying block reward.
//
// Unit-system note (a deliberate judgment call, not a literal
// transcription): the spec's example numbers (1.00/0.80/0.60/0.45/
// 0.30 CCT) use two decimal places, but every CCT amount in this
// ledger is a whole-unit uint64 throughout - no fractional-CCT
// support exists anywhere, and adding one would be a much larger,
// separate migration touching every balance/fee in the system. This
// schedule instead applies the SAME relative decay curve
// (1.00x/0.80x/0.60x/0.45x/0.30x) to the existing real, already-live
// ProduceBlockReward (10 CCT) as the Year-1 baseline, so real
// validators' current income isn't silently cut 10x by this change:
// Year 1 = 10 CCT, Year 2 = 8, Year 3 = 6, Year 4 = 5 (0.45x,
// rounded), Year 5+ = 3 (0.30x, rounded, held flat thereafter - the
// spec only defines 5 years).
var blockRewardSchedule = []blockRewardEra{
	{startYear: 0, rewardCCT: ProduceBlockReward},          // Year 1: 10 CCT (1.00x)
	{startYear: 1, rewardCCT: ProduceBlockReward * 8 / 10}, // Year 2: 8 CCT (0.80x)
	{startYear: 2, rewardCCT: ProduceBlockReward * 6 / 10}, // Year 3: 6 CCT (0.60x)
	{startYear: 3, rewardCCT: 5},                           // Year 4: 5 CCT (0.45x, rounded)
	{startYear: 4, rewardCCT: 3},                           // Year 5+: 3 CCT (0.30x, rounded) - floor
}

const secondsPerYear = 365 * 24 * 3600

// blockRewardForTimestamp returns PoSR's controlled-inflation block
// reward for the era containing ts, anchored to the chain's own
// genesis block timestamp (block index 0) - never wall-clock, so
// every node computing this for the same block reaches an identical
// result (block timestamps are already part of the signed,
// hash-linked block every node agrees on).
func blockRewardForTimestamp(ts int64) uint64 {
	if blockchain == nil || len(blockchain.Blocks) == 0 {
		return ProduceBlockReward
	}
	genesisTs := blockchain.Blocks[0].Timestamp
	yearsElapsed := (ts - genesisTs) / secondsPerYear
	if yearsElapsed < 0 {
		yearsElapsed = 0
	}

	reward := blockRewardSchedule[0].rewardCCT
	for _, era := range blockRewardSchedule {
		if yearsElapsed >= era.startYear {
			reward = era.rewardCCT
		}
	}
	return reward
}

// genesisPoolAddress is PoSR's synthetic escrow-style address for a
// named genesis pool - same convention as stakeEscrowAddress/
// cctTransferFeePoolAddress elsewhere in this file. Foundation
// Treasury deliberately does NOT get one of these - it reuses the
// existing, already-built Treasury feature's real operator-controlled
// address (treasuryStore.Treasury.FoundationAddress) so "Foundation
// Treasury" stays one concept, not two.
func genesisPoolAddress(name string) string {
	return "POOL:" + name
}

const (
	genesisPoolValidatorReward     = "VALIDATOR_REWARD"
	genesisPoolEcosystemDev        = "ECOSYSTEM_DEV"
	genesisPoolGovernanceTreasury  = "GOVERNANCE_TREASURY"
	genesisPoolCommunityIncentives = "COMMUNITY_INCENTIVES"

	// genesisPoolTotalMint (1,000,000,000 CCT) and the 40/25/15/10/10
	// split below are locked per the user's explicit approval
	// (2026-07) - a slice of MaxTotalCCTSupply, not the whole cap,
	// deliberately leaving ~4B headroom for the declining
	// block-reward schedule to mint into over subsequent years.
	genesisPoolValidatorRewardAmount     = 400_000_000
	genesisPoolFoundationTreasuryAmount  = 250_000_000
	genesisPoolEcosystemDevAmount        = 150_000_000
	genesisPoolGovernanceTreasuryAmount  = 100_000_000
	genesisPoolCommunityIncentivesAmount = 100_000_000
)

// HandleAllocateGenesisPools triggers PoSR's one-time genesis pool
// mint - admin-only, recorded as a self-signed TxGenesisPoolAllocation
// (same trust model as HandleTokenMint) so it replicates identically
// to every node rather than only mutating whichever node received
// this HTTP request.
func HandleAllocateGenesisPools(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if !requireAdmin(r) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "admin token required"})
		return
	}

	if treasuryStore.Treasury.GenesisPoolsAllocated {
		http.Error(w, "genesis pools have already been allocated", http.StatusConflict)
		return
	}

	data := []byte("{}")
	tx := recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxGenesisPoolAllocation, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// applyGenesisPoolAllocation is the apply-side counterpart to
// HandleAllocateGenesisPools - idempotent via
// core.Treasury.GenesisPoolsAllocated (this chain has been running
// for a long time already, so there's no true "genesis" moment to
// hook into programmatically; this flag is what makes the deliberate,
// explicit admin action safe to fire at most once). Mints via
// mintCCTCapped so this respects MaxTotalCCTSupply exactly like every
// other CCT mint path.
func applyGenesisPoolAllocation(tx core.Transaction) {
	if treasuryStore.Treasury.GenesisPoolsAllocated {
		return
	}

	mintCCTCapped(genesisPoolAddress(genesisPoolValidatorReward), genesisPoolValidatorRewardAmount)
	mintCCTCapped(treasuryStore.Treasury.FoundationAddress, genesisPoolFoundationTreasuryAmount)
	mintCCTCapped(genesisPoolAddress(genesisPoolEcosystemDev), genesisPoolEcosystemDevAmount)
	mintCCTCapped(genesisPoolAddress(genesisPoolGovernanceTreasury), genesisPoolGovernanceTreasuryAmount)
	mintCCTCapped(genesisPoolAddress(genesisPoolCommunityIncentives), genesisPoolCommunityIncentivesAmount)

	treasuryStore.Treasury.GenesisPoolsAllocated = true
}

package api

import (
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// MaxTotalCCTSupplyWhole is PoSR's lifetime CCT supply cap, in whole
// CCT - 10,000,000,000 (10B). Rescaled up from the original 5B cap as
// part of the 18-decimal migration: the ~1B already-minted genesis
// pools (see genesisPool*Amount below) needed to land as a real
// "controlled portion" of the new ceiling, not the whole thing, so the
// ceiling moved up rather than genesis being shrunk - preserves every
// existing balance exactly via the in-place decimal conversion (see
// applyTokenomicsDecimalMigration) while still leaving ~90% headroom
// for the declining block-reward schedule to mint into over
// subsequent years, the same ~10-20%-genesis/rest-for-inflation shape
// the original 1B-genesis-under-5B-cap ratio already had.
const MaxTotalCCTSupplyWhole = 10_000_000_000

// maxTotalCCTSupplySmallest is MaxTotalCCTSupplyWhole expressed in
// CCT's smallest unit (18 decimals) - computed once, compared against
// cctStore.TotalSupply by mintCCTCapped.
var maxTotalCCTSupplySmallest = core.CctWholeToSmallest(MaxTotalCCTSupplyWhole)

// mintCCTCapped mints up to amount (smallest-unit CCT) to address,
// clamped so cctStore's TotalSupply never exceeds
// maxTotalCCTSupplySmallest. Returns the amount actually minted (as a
// smallest-unit decimal string; may be less than requested, or "0"
// once at the cap) - every CCT-minting call site in this package must
// route through this rather than calling cctStore.Mint directly.
func mintCCTCapped(address string, amount string) string {
	requested, err := core.ParseCctSmallest(amount)
	if err != nil || requested.Sign() == 0 {
		return "0"
	}

	supply, err := core.ParseCctSmallest(cctStore.TotalSupply)
	if err != nil {
		supply = big.NewInt(0)
	}
	if supply.Cmp(maxTotalCCTSupplySmallest) >= 0 {
		return "0"
	}
	headroom := new(big.Int).Sub(maxTotalCCTSupplySmallest, supply)
	toMint := requested
	if toMint.Cmp(headroom) > 0 {
		toMint = headroom
	}

	amountStr := core.FormatCctSmallest(toMint)
	cctStore.Mint(address, amountStr)
	return amountStr
}

// mintCCTCappedWhole is mintCCTCapped's convenience wrapper for
// call sites that still think in whole CCT (genesis pool constants,
// block-reward schedule amounts) - converts once at the boundary via
// core.CctWholeToSmallest rather than making every caller do it.
// Returns the amount actually minted, in smallest units (a decimal
// string), same as mintCCTCapped.
func mintCCTCappedWhole(address string, wholeAmount uint64) string {
	return mintCCTCapped(address, core.FormatCctSmallest(core.CctWholeToSmallest(wholeAmount)))
}

// hasCctBalance reports whether address's live CCT balance (cctStore)
// covers at least wholeAmount whole CCT - the whole-unit-friendly
// convenience wrapper most fast-fail preview checks in this package
// want, so call sites don't each need to hand-roll a ParseCctSmallest/
// big.Int.Cmp dance for a plain "do they have enough" check.
func hasCctBalance(address string, wholeAmount uint64) bool {
	balance, err := core.ParseCctSmallest(cctStore.BalanceOf(address))
	if err != nil {
		return false
	}
	return balance.Cmp(core.CctWholeToSmallest(wholeAmount)) >= 0
}

// transferCctWhole moves wholeAmount whole CCT from -> to via cctStore,
// converting at the boundary - the whole-unit convenience wrapper for
// apply-side fee/payout call sites that still think in whole CCT.
func transferCctWhole(from, to string, wholeAmount uint64) bool {
	return cctStore.Transfer(from, to, core.FormatCctSmallest(core.CctWholeToSmallest(wholeAmount)))
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

	signedAuth, mode, err := requireAdminAuthed(r, nil)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if treasuryStore.Treasury.GenesisPoolsAllocated {
		http.Error(w, "genesis pools have already been allocated", http.StatusConflict)
		return
	}

	data := []byte("{}")
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxGenesisPoolAllocation, string(data))

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

	mintCCTCappedWhole(genesisPoolAddress(genesisPoolValidatorReward), genesisPoolValidatorRewardAmount)
	mintCCTCappedWhole(treasuryStore.Treasury.FoundationAddress, genesisPoolFoundationTreasuryAmount)
	mintCCTCappedWhole(genesisPoolAddress(genesisPoolEcosystemDev), genesisPoolEcosystemDevAmount)
	mintCCTCappedWhole(genesisPoolAddress(genesisPoolGovernanceTreasury), genesisPoolGovernanceTreasuryAmount)
	mintCCTCappedWhole(genesisPoolAddress(genesisPoolCommunityIncentives), genesisPoolCommunityIncentivesAmount)

	treasuryStore.Treasury.GenesisPoolsAllocated = true
}

package api

import (
	"sort"

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
)

// processReputationMining mints REPT ("Reputation Credits") to every
// wallet in good standing, proportional to its Mining Power score
// (miningPowerScore, api/mining_power.go) - the continuous-mining half
// of the Reputation Credit spec, alongside the existing one-time
// tier-crossing reward (core/reputation_reward.go).
//
// Gated to run once every miningEpochBlocks (keyed purely off
// block.Index, never wall-clock) rather than every block:
// processDormantWallets already iterates every wallet every block
// read-only; this sweep adds a WRITE (token mint) per eligible wallet,
// proportionally heavier at scale, so it's bounded to a small fraction
// of blocks. Still O(all wallets) once per epoch - a known, disclosed
// scaling limitation for now (see audit/KNOWN_GAPS.md), not solved
// here.
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
	if block.Index == 0 || block.Index%miningEpochBlocks != 0 {
		return
	}

	var addrs []string
	for addr, w := range walletStore.Wallets {
		if w.LastActivityAt == 0 {
			continue
		}
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs) // same determinism convention as processDormantWallets/processLoanDefaults

	for _, addr := range addrs {
		amount := baseEpochMiningEmission * miningPowerScore(addr, block.Timestamp) / 100
		if amount > 0 {
			reptStore.Mint(addr, amount)
		}
	}
}

package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestProcessReputationMining_SkipsNonEpochBlocks(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-sweep-test-wallet"
	walletStore.Get(addr).Reputation = 100
	walletStore.Get(addr).LastActivityAt = 500

	processReputationMining(core.Block{Index: miningEpochBlocks - 1, Timestamp: 1000})

	if got := reptStore.BalanceOf(addr); got != 0 {
		t.Fatalf("expected no mint on a non-epoch block, got %d REPT", got)
	}
}

func TestProcessReputationMining_SkipsBlockZero(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-sweep-genesis-wallet"
	walletStore.Get(addr).Reputation = 100
	walletStore.Get(addr).LastActivityAt = 500

	// block.Index % miningEpochBlocks == 0 is also true at Index==0,
	// but that's genesis, not a real epoch boundary - must be
	// explicitly excluded.
	processReputationMining(core.Block{Index: 0, Timestamp: 1000})

	if got := reptStore.BalanceOf(addr); got != 0 {
		t.Fatalf("expected no mint at block 0 (genesis, not a real epoch), got %d REPT", got)
	}
}

func TestProcessReputationMining_SkipsNeverActiveWallets(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-sweep-never-active-wallet"
	walletStore.Get(addr).Reputation = 100
	// LastActivityAt left at its zero value - never signed anything,
	// e.g. a synthetic pool/escrow address.

	processReputationMining(core.Block{Index: miningEpochBlocks, Timestamp: 1000})

	if got := reptStore.BalanceOf(addr); got != 0 {
		t.Fatalf("expected no mint for a wallet that has never been active, got %d REPT", got)
	}
}

func TestProcessReputationMining_MintsProportionalToScore(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-sweep-active-wallet"
	walletStore.Get(addr).Reputation = 100
	walletStore.Get(addr).LastActivityAt = 500

	asOf := int64(1000)
	processReputationMining(core.Block{Index: miningEpochBlocks, Timestamp: asOf})

	want := baseEpochMiningEmission * miningPowerScore(addr, asOf) / 100
	if want == 0 {
		t.Fatalf("test setup produced a zero expected mint - not a meaningful test")
	}
	if got := reptStore.BalanceOf(addr); got != want {
		t.Fatalf("expected %d REPT minted proportional to Mining Power score, got %d", want, got)
	}
}

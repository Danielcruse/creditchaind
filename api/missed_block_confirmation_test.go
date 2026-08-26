package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestRequiredMissedBlockConfirmations_MajorityFormula(t *testing.T) {
	cases := []struct {
		n        int
		expected int
	}{
		{0, 1}, // no active validators known yet - can't do better than requiring the one report that exists
		{1, 1}, // genuinely a single-node network - nobody else to corroborate with
		{2, 2}, // strict majority of 2 is both (>1 means 2, not 1)
		{3, 2}, // majority of 3
		{4, 3}, // majority of 4
		{5, 3}, // majority of 5
		{10, 6},
	}
	for _, c := range cases {
		if got := requiredMissedBlockConfirmations(c.n); got != c.expected {
			t.Errorf("requiredMissedBlockConfirmations(%d) = %d, want %d", c.n, got, c.expected)
		}
	}
}

func TestDistinctActiveValidatorNodeCount_CountsUniqueNodesAmongActiveValidators(t *testing.T) {
	resetPoSRTestState()

	if got := distinctActiveValidatorNodeCount(); got != 1 {
		t.Fatalf("expected a floor of 1 with zero validators registered, got %d", got)
	}

	validatorStore.Register("val-a", "node-1", "pubkey-a")
	validatorStore.Register("val-b", "node-1", "pubkey-b") // same node as val-a - delegated, not a distinct node
	validatorStore.Register("val-c", "node-2", "pubkey-c")

	if got := distinctActiveValidatorNodeCount(); got != 2 {
		t.Fatalf("expected 2 distinct nodes (node-1, node-2) across 3 validators, got %d", got)
	}

	inactive := validatorStore.Register("val-d", "node-3", "pubkey-d")
	inactive.Active = false

	if got := distinctActiveValidatorNodeCount(); got != 2 {
		t.Fatalf("expected an inactive validator's node to not count, got %d", got)
	}
}

func applyMissedBlockReport(reporterAuthAddr, validatorAddr, roundHash string) {
	data, _ := json.Marshal(slashReportData{
		Reason:           "missed_block",
		ValidatorAddress: validatorAddr,
		RoundHash:        roundHash,
	})
	applySlashReport(core.Transaction{
		ID:          "report-" + reporterAuthAddr + "-" + roundHash,
		AuthAddress: reporterAuthAddr,
		Data:        string(data),
	})
}

// TestApplySlashReport_MissedBlock_RequiresCorroborationWhenMultipleNodesExist
// is the core regression test for the M-of-N fix: with 3 distinct
// nodes known (via 3 differently-node-delegated active validators),
// requiredMissedBlockConfirmations(3) == 2, so a single reporting
// node's claim must NOT be enough on its own to slash - closing the
// exact single-reporter-trust gap disclosed in audit/KNOWN_GAPS.md.
func TestApplySlashReport_MissedBlock_RequiresCorroborationWhenMultipleNodesExist(t *testing.T) {
	resetPoSRTestState()

	const target = "posr-mofn-target"
	w := walletStore.Get(target)
	w.Reputation = 100
	stakingStore.Stakes[target] = map[string]uint64{"CCT": 10000}
	cctStore.Mint(stakeEscrowAddress("CCT"), cctW(10000))

	// 3 distinct nodes known network-wide (the target validator plus
	// two others delegated to different nodes) - majority of 3 is 2.
	validatorStore.Register(target, "node-1", "abc")
	validatorStore.Register("other-validator-1", "node-2", "def")
	validatorStore.Register("other-validator-2", "node-3", "ghi")

	if required := requiredMissedBlockConfirmations(distinctActiveValidatorNodeCount()); required != 2 {
		t.Fatalf("test setup issue: expected required confirmations 2 for 3 known nodes, got %d", required)
	}

	// First, single report - must NOT slash yet.
	applyMissedBlockReport("reporter-node-1", target, "round-x")

	if got := stakingStore.Stakes[target]["CCT"]; got != 10000 {
		t.Fatalf("expected stake untouched after only 1 of 2 required corroborating reports, got %d remaining", got)
	}
	if status, _ := validatorStore.Get(target); status.SlashCount != 0 {
		t.Fatalf("expected SlashCount 0 after only 1 report, got %d", status.SlashCount)
	}

	// Same reporter resubmitting the identical event must not count twice.
	applyMissedBlockReport("reporter-node-1", target, "round-x")
	if got := stakingStore.Stakes[target]["CCT"]; got != 10000 {
		t.Fatalf("expected stake still untouched after a duplicate report from the same reporter, got %d remaining", got)
	}

	// A second, genuinely distinct reporting node corroborates - now
	// the threshold (2) is crossed and the penalty applies exactly once.
	applyMissedBlockReport("reporter-node-2", target, "round-x")

	expectedSlash := uint64(10000) * core.MissedBlockStakeSlashBps / 10000
	if got := stakingStore.Stakes[target]["CCT"]; got != 10000-expectedSlash {
		t.Fatalf("expected stake reduced by %d once 2 independent reports corroborated the same event, got %d remaining", expectedSlash, got)
	}
	if status, _ := validatorStore.Get(target); status.SlashCount != core.SlashStrikesMissedBlock {
		t.Fatalf("expected exactly one strike recorded, got %d", status.SlashCount)
	}

	// A third, late-arriving corroborating report for the same
	// already-resolved event must be a no-op, not a second penalty.
	applyMissedBlockReport("reporter-node-3", target, "round-x")
	if got := stakingStore.Stakes[target]["CCT"]; got != 10000-expectedSlash {
		t.Fatalf("expected no further stake reduction from a late corroborating report on an already-applied event, got %d remaining", got)
	}
	if status, _ := validatorStore.Get(target); status.SlashCount != core.SlashStrikesMissedBlock {
		t.Fatalf("expected SlashCount to stay at %d, got %d", core.SlashStrikesMissedBlock, status.SlashCount)
	}
}

// TestApplySlashReport_MissedBlock_SingleNodeNetworkStillSlashesOnOneReport
// confirms the degrade-gracefully floor: a genuinely single-node
// network (only the target validator itself known) has nobody to
// corroborate with, so 1 report is still sufficient - matching this
// codebase's existing test (TestApplySlashReport_MissedBlock_
// SlashesStakeAndBansAfterThreshold in posr_test.go) and confirming
// this fix doesn't regress small/bootstrap networks.
func TestApplySlashReport_MissedBlock_SingleNodeNetworkStillSlashesOnOneReport(t *testing.T) {
	resetPoSRTestState()

	const target = "posr-mofn-single-node"
	w := walletStore.Get(target)
	w.Reputation = 100
	stakingStore.Stakes[target] = map[string]uint64{"CCT": 10000}
	cctStore.Mint(stakeEscrowAddress("CCT"), cctW(10000))

	validatorStore.Register(target, "node-1", "abc")

	applyMissedBlockReport("reporter-node-1", target, "round-y")

	expectedSlash := uint64(10000) * core.MissedBlockStakeSlashBps / 10000
	if got := stakingStore.Stakes[target]["CCT"]; got != 10000-expectedSlash {
		t.Fatalf("expected a single-node network to still slash on 1 report, got %d remaining (expected %d)", got, 10000-expectedSlash)
	}
}

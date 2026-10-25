package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestApplyGovernance_EmergencyCreateRejectsNonAllowlistedParamKey(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "emg-proposer-bad-key", 100, 0)
	tx := core.Transaction{
		ID:   "emg-create-bad-key",
		From: "emg-proposer-bad-key",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Sneaky change","kind":"EMERGENCY","paramKey":"CCTTransferFee","paramValue":0}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created for a non-allowlisted EMERGENCY paramKey, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_EmergencyCreateRejectsInvalidParamValue(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "emg-proposer-bad-value", 100, 0)
	tx := core.Transaction{
		ID:   "emg-create-bad-value",
		From: "emg-proposer-bad-value",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Pause?","kind":"EMERGENCY","paramKey":"BridgePaused","paramValue":2}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created for paramValue outside {0,1}, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_EmergencyCreateGetsShorterWindowAndHigherQuorum(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "emg-proposer-quorum", 100, 0)
	tx := core.Transaction{
		ID:        "emg-create-quorum",
		From:      "emg-proposer-quorum",
		Type:      core.TxGovernance,
		Timestamp: 1000,
		Data:      `{"action":"create","title":"Pause the bridge","kind":"EMERGENCY","paramKey":"BridgePaused","paramValue":1}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 1 {
		t.Fatalf("expected exactly one proposal created, got %d", len(proposalStore.Proposals))
	}
	var p *core.Proposal
	for _, v := range proposalStore.Proposals {
		p = v
	}
	if p.Quorum != EmergencyProposalQuorum {
		t.Fatalf("expected quorum %d (doubled), got %d", EmergencyProposalQuorum, p.Quorum)
	}
	if want := int64(1000) + core.EmergencyProposalVotingWindowSeconds; p.Deadline != want {
		t.Fatalf("expected the shorter 24h emergency voting window, deadline=%d, got %d", want, p.Deadline)
	}
}

// TestEmergencyProposal_EndToEnd_PauseThenResumeBridge is the core
// integration test: a genuinely passed EMERGENCY proposal pauses the
// bridge only after its short timelock elapses, never in the same
// block it finalizes, and a second EMERGENCY proposal can resume it
// the same way.
func TestEmergencyProposal_EndToEnd_PauseThenResumeBridge(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "emg-voter", 100, core.StakeCap)

	pause := &core.Proposal{
		ID:         "prop-emg-pause",
		CreatedBy:  "emg-voter",
		Open:       true,
		Quorum:     10,
		Deadline:   1000,
		Kind:       core.ProposalKindEmergency,
		ParamKey:   BridgePausedKey,
		ParamValue: 1,
	}
	proposalStore.Create(pause)
	proposalStore.Vote(pause.ID, "emg-voter", true, votingWeight("emg-voter"), 0)
	processProposalFinalization(1000)

	if pause.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected the pause proposal to pass, got %q", pause.Status)
	}
	if bridgePaused() {
		t.Fatalf("expected the bridge to still be unpaused before the timelock elapses")
	}

	// A deposit confirmed before the pause takes effect must still go
	// through normally.
	depositTx := core.Transaction{
		ID:        "emg-deposit-before-pause",
		Type:      core.TxBridgeDeposit,
		Timestamp: pause.ExecuteAfter - 1,
		Data:      `{"creditChainAddress":"not-a-real-account","newBalanceWei":"1000"}`,
	}
	applyBridgeDeposit(depositTx) // no-op either way (account doesn't exist) - just must not panic

	processProposalExecution(pause.ExecuteAfter)
	if !bridgePaused() {
		t.Fatalf("expected the bridge to be paused once the emergency timelock elapses")
	}

	resume := &core.Proposal{
		ID:         "prop-emg-resume",
		CreatedBy:  "emg-voter",
		Open:       true,
		Quorum:     10,
		Deadline:   pause.ExecuteAfter + 1000,
		Kind:       core.ProposalKindEmergency,
		ParamKey:   BridgePausedKey,
		ParamValue: 0,
	}
	proposalStore.Create(resume)
	proposalStore.Vote(resume.ID, "emg-voter", true, votingWeight("emg-voter"), 0)
	processProposalFinalization(resume.Deadline)
	processProposalExecution(resume.ExecuteAfter)

	if bridgePaused() {
		t.Fatalf("expected the bridge to be unpaused once the resume proposal executes")
	}
}

func TestApplyVaultBurn_RefusedWhileBridgePaused(t *testing.T) {
	resetGovernanceTestState()
	governanceParamsStore.SetAt(BridgePausedKey, 1, 0)

	const depositor, pubKey = "vault-burn-paused", "vault-burn-paused-pubkey"
	balanceAddr := resolveBalanceAddress(depositor, pubKey, "ETH_CCC1")
	ethCcc1Store.Mint(balanceAddr, "1000000000000000000")

	tx := core.Transaction{
		ID:            "vault-burn-while-paused",
		AuthAddress:   depositor,
		AuthPublicKey: pubKey,
		Type:          core.TxVaultBurn,
		Data:          `{"chain":"ETH","destinationExtAddress":"0xdeadbeef","amountSmallestUnit":"500000000000000000"}`,
	}
	applyVaultBurn(tx)

	if got := ethCcc1Store.BalanceOf(balanceAddr); got != "1000000000000000000" {
		t.Fatalf("expected the burn to be refused while the bridge is paused, balance unchanged, got %s", got)
	}
	if _, ok := vaultReleaseApprovalStore.Get(tx.ID); ok {
		t.Fatalf("expected no release-approval record opened while the bridge is paused")
	}
}

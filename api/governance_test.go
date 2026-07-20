package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetGovernanceTestState extends resetApplyTestState with the
// governance-specific package vars it doesn't already reset.
func resetGovernanceTestState() {
	resetApplyTestState()
	governanceParamsStore = core.NewGovernanceParamsStore()
	governanceParamsStore.Seed("ValidatorRegisterFeeCCT", ValidatorRegisterFeeCCT)
	governanceParamsStore.Seed("CCTTransferFee", CCTTransferFee)
	core.ActivatedProtocolVersion = ""
}

func TestVotingWeight_ZeroForNonActivatedWallet(t *testing.T) {
	resetGovernanceTestState()

	const addr = "gov-test-not-activated"
	w := walletStore.Get(addr)
	w.Reputation = 90
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 50000}

	if got := votingWeight(addr); got != 0 {
		t.Fatalf("expected 0 weight for a non-activated wallet regardless of stake/reputation, got %d", got)
	}
}

func TestVotingWeight_FloorOneForActivatedZeroStakeZeroReputation(t *testing.T) {
	resetGovernanceTestState()

	const addr = "gov-test-floor"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Reputation = 0

	if got := votingWeight(addr); got != 1 {
		t.Fatalf("expected the floor of 1 for an activated wallet with zero stake and zero reputation, got %d", got)
	}
}

func TestVotingWeight_ScalesWithStakeAndReputation(t *testing.T) {
	resetGovernanceTestState()

	const addr = "gov-test-scaled"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Reputation = 100
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": core.StakeCap}

	// normalizedStake=100, reputation=100 -> 1 + 100*40/100 + 100*60/100 = 1+40+60 = 101
	if got := votingWeight(addr); got != 101 {
		t.Fatalf("expected 101 at full stake/reputation, got %d", got)
	}

	// Stake beyond the cap must not push weight past what a fully-capped
	// stake already gives.
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": core.StakeCap * 10}
	if got := votingWeight(addr); got != 101 {
		t.Fatalf("expected normalizedStake to clamp at 100 beyond the cap, got %d", got)
	}
}

func newActivatedVoter(t *testing.T, addr string, reputation, stakeCCT uint64) {
	t.Helper()
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Reputation = reputation
	if stakeCCT > 0 {
		stakingStore.Stakes[addr] = map[string]uint64{"CCT": stakeCCT}
	}
}

func TestProcessProposalFinalization_PassesWhenQuorumMetAndYesWins(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "voter-a", 100, core.StakeCap)
	newActivatedVoter(t, "voter-b", 100, core.StakeCap)

	p := &core.Proposal{
		ID:        "prop-pass",
		CreatedBy: "proposer-a",
		Open:      true,
		Quorum:    100,
		Deadline:  1000,
	}
	proposalStore.Create(p)

	if _, err := proposalStore.Vote(p.ID, "voter-a", true, votingWeight("voter-a"), 0); err != nil {
		t.Fatalf("vote a: %v", err)
	}
	if _, err := proposalStore.Vote(p.ID, "voter-b", true, votingWeight("voter-b"), 0); err != nil {
		t.Fatalf("vote b: %v", err)
	}

	processProposalFinalization(1000)

	if p.Open {
		t.Fatalf("expected proposal to be closed after its deadline")
	}
	if p.Status != core.ProposalStatusPassed {
		t.Fatalf("expected PASSED, got %q (yes=%d no=%d quorum=%d)", p.Status, p.YesVotes, p.NoVotes, p.Quorum)
	}
}

func TestProcessProposalFinalization_FailsWhenQuorumMetButNoWins(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "voter-a", 100, core.StakeCap)
	newActivatedVoter(t, "voter-b", 100, core.StakeCap)

	p := &core.Proposal{
		ID:        "prop-no-wins",
		CreatedBy: "proposer-a",
		Open:      true,
		Quorum:    100,
		Deadline:  1000,
	}
	proposalStore.Create(p)

	proposalStore.Vote(p.ID, "voter-a", false, votingWeight("voter-a"), 0)
	proposalStore.Vote(p.ID, "voter-b", true, 1, 0)

	processProposalFinalization(1000)

	if p.Status != core.ProposalStatusFailed {
		t.Fatalf("expected FAILED when No >= Yes despite quorum, got %q", p.Status)
	}
}

func TestProcessProposalFinalization_FailsWhenQuorumNeverReached(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "voter-a", 100, 0)

	p := &core.Proposal{
		ID:        "prop-no-quorum",
		CreatedBy: "proposer-a",
		Open:      true,
		Quorum:    10000,
		Deadline:  1000,
	}
	proposalStore.Create(p)

	proposalStore.Vote(p.ID, "voter-a", true, votingWeight("voter-a"), 0)

	processProposalFinalization(1000)

	if p.Status != core.ProposalStatusFailed {
		t.Fatalf("expected FAILED when quorum is never reached regardless of Yes/No split, got %q", p.Status)
	}
}

func TestProcessProposalFinalization_IdempotentOnAlreadyClosedProposal(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "voter-a", 100, core.StakeCap)

	p := &core.Proposal{
		ID:        "prop-idempotent",
		CreatedBy: "voter-a",
		Open:      true,
		Quorum:    1,
		Deadline:  1000,
	}
	proposalStore.Create(p)
	proposalStore.Vote(p.ID, "voter-a", true, votingWeight("voter-a"), 0)

	processProposalFinalization(1000)
	yesAfterFirst := p.YesVotes
	statusAfterFirst := p.Status

	rept := reptStore.BalanceOf("voter-a")

	// A second call at a later timestamp must be a no-op for this
	// already-closed proposal - Open is already false, so the sweep's
	// own filter should skip it entirely (no re-mint, no status flip).
	processProposalFinalization(2000)

	if p.YesVotes != yesAfterFirst || p.Status != statusAfterFirst {
		t.Fatalf("expected a second finalization pass to be a no-op, got yes=%d status=%q", p.YesVotes, p.Status)
	}
	if got := reptStore.BalanceOf("voter-a"); got != rept {
		t.Fatalf("expected no additional REPT minted on the idempotent second pass, got %d (was %d)", got, rept)
	}
}

func TestProcessProposalFinalization_MintsREPTForProposerAndVotersOnPassedOnly(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "proposer-x", 100, 0)
	newActivatedVoter(t, "voter-yes", 100, core.StakeCap)
	newActivatedVoter(t, "voter-no", 100, core.StakeCap)

	passing := &core.Proposal{
		ID:        "prop-rept-pass",
		CreatedBy: "proposer-x",
		Open:      true,
		Quorum:    10,
		Deadline:  1000,
	}
	proposalStore.Create(passing)
	proposalStore.Vote(passing.ID, "voter-yes", true, votingWeight("voter-yes"), 0)
	proposalStore.Vote(passing.ID, "voter-no", false, 1, 0)

	processProposalFinalization(1000)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected passing proposal to actually pass, got %q", passing.Status)
	}
	if got := reptStore.BalanceOf("proposer-x"); got != ProposalPassedProposerReward {
		t.Fatalf("expected proposer to receive %d REPT, got %d", ProposalPassedProposerReward, got)
	}
	if got := reptStore.BalanceOf("voter-yes"); got != ProposalPassedVoterReward {
		t.Fatalf("expected yes-voter to receive %d REPT, got %d", ProposalPassedVoterReward, got)
	}
	if got := reptStore.BalanceOf("voter-no"); got != ProposalPassedVoterReward {
		t.Fatalf("expected no-voter to receive %d REPT too (participation, not winning-side only), got %d", got, ProposalPassedVoterReward)
	}

	// A FAILED proposal (quorum never reached) mints nothing.
	newActivatedVoter(t, "proposer-y", 100, 0)
	newActivatedVoter(t, "voter-z", 100, 0)
	failing := &core.Proposal{
		ID:        "prop-rept-fail",
		CreatedBy: "proposer-y",
		Open:      true,
		Quorum:    99999,
		Deadline:  1000,
	}
	proposalStore.Create(failing)
	proposalStore.Vote(failing.ID, "voter-z", true, votingWeight("voter-z"), 0)

	processProposalFinalization(1000)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("setup issue: expected failing proposal to actually fail, got %q", failing.Status)
	}
	if got := reptStore.BalanceOf("proposer-y"); got != 0 {
		t.Fatalf("expected no REPT for a FAILED proposal's proposer, got %d", got)
	}
	if got := reptStore.BalanceOf("voter-z"); got != 0 {
		t.Fatalf("expected no REPT for a FAILED proposal's voters, got %d", got)
	}
}

func TestProcessProposalFinalization_ParamChangePassingUpdatesGovernanceParamsStore(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "voter-param", 100, core.StakeCap)

	p := &core.Proposal{
		ID:         "prop-param-change",
		CreatedBy:  "voter-param",
		Open:       true,
		Quorum:     10,
		Deadline:   1000,
		Kind:       core.ProposalKindParamChange,
		ParamKey:   "CCTTransferFee",
		ParamValue: 7,
	}
	proposalStore.Create(p)
	proposalStore.Vote(p.ID, "voter-param", true, votingWeight("voter-param"), 0)

	processProposalFinalization(1000)

	if p.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected the PARAM_CHANGE proposal to pass, got %q", p.Status)
	}
	if got := governanceParam("CCTTransferFee", CCTTransferFee); got != 7 {
		t.Fatalf("expected governanceParam(\"CCTTransferFee\") to reflect the passed proposal's value 7, got %d", got)
	}

	// A subsequent fee-charging transfer must actually use the new fee.
	const from = "param-change-sender"
	tokenStore.Mint(from, 100+7)
	tx := core.Transaction{ID: "param-change-transfer", From: from, To: "param-change-recipient", Amount: 100, Type: core.TxExchange}
	applyTransfer(tx)
	if got := tokenStore.BalanceOf(from); got != 0 {
		t.Fatalf("expected sender balance 0 after paying amount(100)+new fee(7) out of a 107 balance, got %d", got)
	}
}

func TestProcessProposalFinalization_VersionActivationPassingUpdatesActivatedProtocolVersion(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "voter-version", 100, core.StakeCap)

	p := &core.Proposal{
		ID:            "prop-version-activation",
		CreatedBy:     "voter-version",
		Open:          true,
		Quorum:        10,
		Deadline:      1000,
		Kind:          core.ProposalKindVersionActivation,
		TargetVersion: "v2",
	}
	proposalStore.Create(p)
	proposalStore.Vote(p.ID, "voter-version", true, votingWeight("voter-version"), 0)

	processProposalFinalization(1000)

	if p.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected the VERSION_ACTIVATION proposal to pass, got %q", p.Status)
	}
	if core.ActivatedProtocolVersion != "v2" {
		t.Fatalf("expected core.ActivatedProtocolVersion to be \"v2\", got %q", core.ActivatedProtocolVersion)
	}
}

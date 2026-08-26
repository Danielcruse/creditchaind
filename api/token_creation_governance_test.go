package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetTokenCreationGovernanceTestState extends resetGovernanceTestState
// with the token-registry state this file's tests also mutate, so each
// test starts from a genuinely clean registry rather than accumulating
// symbols across test runs within the same process.
func resetTokenCreationGovernanceTestState() {
	resetGovernanceTestState()
	tokenRegistry = core.NewTokenRegistry()
	tokenStandardRegistry = core.NewTokenStandardRegistry()
	reptStore = core.NewTokenStore()
}

func TestApplyGovernance_TokenCreationChargesFeeOnValidSubmission(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	const proposer = "tc-fee-proposer"
	cctStore.Mint(proposer, cctW(TokenCreationProposalFeeCCT))
	reptStore.Mint(proposer, TokenCreationMinRC)

	tx := core.Transaction{
		ID:   "tc-create-1",
		From: proposer,
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Make MYTOK","kind":"TOKEN_CREATION","tokenSymbol":"MYTOK","tokenName":"My Token","tokenInitialSupply":1000,"tokenStandard":"C1"}`,
	}
	applyGovernance(tx)

	if got := cctStore.BalanceOf(proposer); got != "0" {
		t.Fatalf("expected the full %d CCT fee charged, got balance %s remaining", TokenCreationProposalFeeCCT, got)
	}
	feeBal, _ := core.ParseCctSmallest(cctStore.BalanceOf(tokenCreationFeeAddress()))
	if feeBal.Sign() == 0 {
		t.Fatalf("expected the fee to land in the token creation fee sink address")
	}

	if len(proposalStore.Proposals) != 1 {
		t.Fatalf("expected exactly one proposal created, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_TokenCreationRejectedInsufficientFeeBalance(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	const proposer = "tc-poor-proposer"
	// Deliberately underfund - less than the fee. RC is funded so this
	// test isolates the CCT-fee rejection specifically.
	cctStore.Mint(proposer, cctW(TokenCreationProposalFeeCCT-1))
	reptStore.Mint(proposer, TokenCreationMinRC)

	tx := core.Transaction{
		ID:   "tc-create-poor",
		From: proposer,
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Make POOR","kind":"TOKEN_CREATION","tokenSymbol":"POORTOK","tokenName":"Poor Token","tokenInitialSupply":1000,"tokenStandard":"C1"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created when the fee can't be covered, got %d", len(proposalStore.Proposals))
	}
	if got := cctStore.BalanceOf(proposer); got != cctW(TokenCreationProposalFeeCCT-1) {
		t.Fatalf("expected the underfunded proposer's balance untouched, got %s", got)
	}
}

func TestApplyGovernance_TokenCreationRejectedForAlreadyRegisteredSymbol(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	tokenRegistry.Register("TAKEN", "Already Here", "someone-else", 100, "C1", "")

	const proposer = "tc-race-proposer"
	cctStore.Mint(proposer, cctW(TokenCreationProposalFeeCCT))
	reptStore.Mint(proposer, TokenCreationMinRC)

	tx := core.Transaction{
		ID:   "tc-create-race",
		From: proposer,
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Make TAKEN","kind":"TOKEN_CREATION","tokenSymbol":"TAKEN","tokenName":"Collides","tokenInitialSupply":1000,"tokenStandard":"C1"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created for an already-registered symbol, got %d", len(proposalStore.Proposals))
	}
	// Fee must not be charged either - the proposal never got created.
	if got := cctStore.BalanceOf(proposer); got != cctW(TokenCreationProposalFeeCCT) {
		t.Fatalf("expected no fee charged when the create itself is rejected, got balance %s", got)
	}
}

func TestFinalizeProposal_TokenCreationRegistersTokenOnlyOnPassed(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	newActivatedVoter(t, "tc-voter", 100, core.StakeCap)

	passing := &core.Proposal{
		ID:                 "prop-tc-pass",
		CreatedBy:           "tc-creator",
		Open:                true,
		Quorum:              10,
		Deadline:            1000,
		Kind:                core.ProposalKindTokenCreation,
		TokenSymbol:         "PASSTOK",
		TokenName:           "Pass Token",
		TokenInitialSupply:  5000,
		TokenStandard:       "C1",
	}
	proposalStore.Create(passing)
	proposalStore.Vote(passing.ID, "tc-voter", true, votingWeight("tc-voter"), 0)

	processProposalFinalization(1000)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected the proposal to pass, got %q", passing.Status)
	}
	meta, exists := tokenRegistry.Meta["PASSTOK"]
	if !exists {
		t.Fatalf("expected PASSTOK to be registered after a genuine PASSED outcome")
	}
	if meta.Creator != "tc-creator" || meta.InitialSupply != 5000 {
		t.Fatalf("expected registered token to carry the proposal's creator/supply, got creator=%q supply=%d", meta.Creator, meta.InitialSupply)
	}
	if got := tokenRegistry.Tokens["PASSTOK"].BalanceOf("tc-creator"); got != 5000 {
		t.Fatalf("expected the initial supply minted to the proposal's creator, got %d", got)
	}

	// A FAILED TOKEN_CREATION proposal must never register anything.
	newActivatedVoter(t, "tc-voter-fail", 100, 0)
	failing := &core.Proposal{
		ID:                "prop-tc-fail",
		CreatedBy:          "tc-creator-2",
		Open:               true,
		Quorum:             99999,
		Deadline:           1000,
		Kind:               core.ProposalKindTokenCreation,
		TokenSymbol:        "FAILTOK",
		TokenName:          "Fail Token",
		TokenInitialSupply: 5000,
		TokenStandard:      "C1",
	}
	proposalStore.Create(failing)
	proposalStore.Vote(failing.ID, "tc-voter-fail", true, votingWeight("tc-voter-fail"), 0)

	processProposalFinalization(1000)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("setup issue: expected the proposal to fail (quorum never reached), got %q", failing.Status)
	}
	if _, exists := tokenRegistry.Meta["FAILTOK"]; exists {
		t.Fatalf("expected FAILTOK to never be registered for a FAILED proposal")
	}
}

func TestApplyTokenCreationFromProposal_SkipsIfSymbolAlreadyRegistered(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	tokenRegistry.Register("RACY", "Original", "original-creator", 111, "C1", "")

	p := &core.Proposal{
		ID:                 "prop-racy",
		CreatedBy:           "late-creator",
		Kind:                core.ProposalKindTokenCreation,
		TokenSymbol:         "RACY",
		TokenName:           "Late Racer",
		TokenInitialSupply:  999,
		TokenStandard:       "C1",
	}

	applyTokenCreationFromProposal(p)

	meta := tokenRegistry.Meta["RACY"]
	if meta.Creator != "original-creator" || meta.InitialSupply != 111 {
		t.Fatalf("expected the already-registered symbol's original owner/supply to be untouched, got creator=%q supply=%d", meta.Creator, meta.InitialSupply)
	}
}

func TestHandleRegisterToken_DirectSelfServicePathRetired(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	req := httptest.NewRequest("POST", "/tokens/register", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	HandleRegisterToken(rec, req)

	if rec.Code != 410 {
		t.Fatalf("expected 410 Gone from the retired direct registration endpoint, got %d", rec.Code)
	}
}

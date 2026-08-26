package api

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetNodeAdmissionTestState extends resetGovernanceTestState with the
// node-admission-specific package vars it doesn't already reset -
// mirrors resetTokenCreationGovernanceTestState's own pattern.
func resetNodeAdmissionTestState() {
	resetGovernanceTestState()
	nodeAdmissionStore = core.NewNodeAdmissionStore()
}

func genEd25519PublicKeyHex(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return hex.EncodeToString(pub)
}

func TestApplyGovernance_NodeAdmissionCreateRejectsMalformedPublicKey(t *testing.T) {
	resetNodeAdmissionTestState()

	newActivatedVoter(t, "na-proposer", 100, 0)

	tx := core.Transaction{
		ID:   "na-create-bad-key",
		From: "na-proposer",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Admit bad node","kind":"NODE_ADMISSION","nodeAdmissionPublicKey":"not-hex-not-a-key","nodeAdmissionNodeId":"node-x"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created for a malformed public key, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_NodeAdmissionCreateRejectsAlreadyAdmittedKey(t *testing.T) {
	resetNodeAdmissionTestState()

	pubHex := genEd25519PublicKeyHex(t)
	nodeAdmissionStore.Grant(&core.NodeAdmission{PublicKey: pubHex, NodeID: "already-in"})

	newActivatedVoter(t, "na-proposer-dup", 100, 0)
	tx := core.Transaction{
		ID:   "na-create-dup",
		From: "na-proposer-dup",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Re-admit","kind":"NODE_ADMISSION","nodeAdmissionPublicKey":"` + pubHex + `","nodeAdmissionNodeId":"node-x"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created for an already-admitted public key, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_NodeAdmissionCreateGetsDoubledQuorum(t *testing.T) {
	resetNodeAdmissionTestState()

	pubHex := genEd25519PublicKeyHex(t)
	newActivatedVoter(t, "na-proposer-quorum", 100, 0)
	tx := core.Transaction{
		ID:   "na-create-quorum",
		From: "na-proposer-quorum",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Admit node","kind":"NODE_ADMISSION","nodeAdmissionPublicKey":"` + pubHex + `","nodeAdmissionNodeId":"node-x"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 1 {
		t.Fatalf("expected exactly one proposal created, got %d", len(proposalStore.Proposals))
	}
	var p *core.Proposal
	for _, v := range proposalStore.Proposals {
		p = v
	}
	if p.Quorum != NodeAdmissionProposalQuorum {
		t.Fatalf("expected quorum %d (doubled), got %d", NodeAdmissionProposalQuorum, p.Quorum)
	}
	if p.Quorum != core.DefaultProposalQuorum*2 {
		t.Fatalf("expected NodeAdmissionProposalQuorum to equal 2x the default, got %d vs default %d", p.Quorum, core.DefaultProposalQuorum)
	}
}

// TestFinalizeProposal_NodeAdmissionGrantsOnlyOnPassed is the core
// integration test: a genuinely passed NODE_ADMISSION proposal must
// grant admission, and a FAILED one must grant nothing - same shape as
// TestFinalizeProposal_TokenCreationRegistersTokenOnlyOnPassed.
func TestFinalizeProposal_NodeAdmissionGrantsOnlyOnPassed(t *testing.T) {
	resetNodeAdmissionTestState()

	voter := "na-voter"
	newActivatedVoter(t, voter, 100, core.StakeCap)
	// votingWeightForNodeAdmission reads GovernanceConviction, not
	// Reputation - newActivatedVoter doesn't know about the new field,
	// so it must be set explicitly here to clear NodeAdmissionMinConviction.
	walletStore.Get(voter).GovernanceConviction = 100

	pubHex := genEd25519PublicKeyHex(t)
	passing := &core.Proposal{
		ID:                     "prop-na-pass",
		CreatedBy:              "na-creator",
		Open:                   true,
		Quorum:                 10,
		Deadline:               1000,
		Kind:                   core.ProposalKindNodeAdmission,
		NodeAdmissionPublicKey: pubHex,
		NodeAdmissionNodeID:    "node-pass",
	}
	proposalStore.Create(passing)
	proposalStore.Vote(passing.ID, voter, true, votingWeightForNodeAdmission(voter), 0)

	processProposalFinalization(1000)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected the proposal to pass, got %q", passing.Status)
	}
	if !nodeAdmissionStore.IsAdmitted(pubHex) {
		t.Fatalf("expected %s to be admitted after a genuine PASSED outcome", pubHex)
	}
	admission, _ := nodeAdmissionStore.Get(pubHex)
	if admission.SponsorAddress != "na-creator" || admission.ProposalID != "prop-na-pass" {
		t.Fatalf("expected the grant to carry the proposal's creator/ID for attribution, got %+v", admission)
	}

	// A FAILED NODE_ADMISSION proposal must never admit anything.
	failPubHex := genEd25519PublicKeyHex(t)
	newActivatedVoter(t, "na-voter-fail", 100, 0)
	walletStore.Get("na-voter-fail").GovernanceConviction = 100
	failing := &core.Proposal{
		ID:                     "prop-na-fail",
		CreatedBy:              "na-creator-2",
		Open:                   true,
		Quorum:                 99999,
		Deadline:               1000,
		Kind:                   core.ProposalKindNodeAdmission,
		NodeAdmissionPublicKey: failPubHex,
		NodeAdmissionNodeID:    "node-fail",
	}
	proposalStore.Create(failing)
	proposalStore.Vote(failing.ID, "na-voter-fail", true, votingWeightForNodeAdmission("na-voter-fail"), 0)

	processProposalFinalization(1000)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("setup issue: expected the proposal to fail (quorum never reached), got %q", failing.Status)
	}
	if nodeAdmissionStore.IsAdmitted(failPubHex) {
		t.Fatalf("expected %s to never be admitted for a FAILED proposal", failPubHex)
	}
}

func TestApplyNodeAdmissionFromProposal_SkipsIfAlreadyAdmitted(t *testing.T) {
	resetNodeAdmissionTestState()

	pubHex := genEd25519PublicKeyHex(t)
	nodeAdmissionStore.Grant(&core.NodeAdmission{PublicKey: pubHex, SponsorAddress: "original-sponsor", ProposalID: "prop-original"})

	p := &core.Proposal{
		ID:                     "prop-late",
		CreatedBy:              "late-sponsor",
		Kind:                   core.ProposalKindNodeAdmission,
		NodeAdmissionPublicKey: pubHex,
	}
	applyNodeAdmissionFromProposal(p, 500)

	admission, _ := nodeAdmissionStore.Get(pubHex)
	if admission.SponsorAddress != "original-sponsor" || admission.ProposalID != "prop-original" {
		t.Fatalf("expected the already-admitted key's original grant to be untouched, got %+v", admission)
	}
}

// TestVotingWeightForNodeAdmission_SybilFloorRequiresRealConviction is
// the Sybil-floor regression test: many freshly-activated, zero-
// conviction wallets must contribute ZERO weight to a NODE_ADMISSION
// vote, unlike the general votingWeight formula's floor-of-1
// convenience - see NodeAdmissionMinConviction's own doc comment for
// why this specific formula deliberately drops that floor.
func TestVotingWeightForNodeAdmission_SybilFloorRequiresRealConviction(t *testing.T) {
	resetGovernanceTestState()

	const addr = "na-sybil-wallet"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	// Deliberately below NodeAdmissionMinConviction, even though the
	// general votingWeight formula's floor-of-1 would still grant SOME
	// weight to a wallet like this.
	w.GovernanceConviction = NodeAdmissionMinConviction - 1

	if got := votingWeightForNodeAdmission(addr); got != 0 {
		t.Fatalf("expected 0 weight for a wallet below NodeAdmissionMinConviction, got %d", got)
	}

	// Confirm the SAME wallet, same address, DOES get real weight from
	// the general formula - proving the zero above is specific to the
	// node-admission gate, not an unrelated bug making the wallet
	// invalid entirely.
	w.Reputation = 50
	if got := votingWeight(addr); got == 0 {
		t.Fatalf("expected the general votingWeight formula to still grant nonzero weight to the same wallet (its floor-of-1), got 0 - this would mean the test itself is broken, not proving the Sybil floor")
	}

	// Crossing the threshold grants real weight.
	w.GovernanceConviction = NodeAdmissionMinConviction
	if got := votingWeightForNodeAdmission(addr); got == 0 {
		t.Fatalf("expected nonzero weight once GovernanceConviction meets NodeAdmissionMinConviction, got 0")
	}
}

// TestVotingWeight_UnchangedByNodeAdmissionAddition pins the general
// votingWeight formula's exact output for a fixed input, proving the
// TSI-restructured 60/40 formula itself was NOT touched by adding
// votingWeightForNodeAdmission alongside it (see votingWeightUsing's
// own doc comment for why generalizing conviction into this formula
// was considered and deliberately rejected).
func TestVotingWeight_UnchangedByNodeAdmissionAddition(t *testing.T) {
	resetGovernanceTestState()

	const addr = "na-regression-wallet"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Reputation = 80
	w.Trust = core.TrustDimensions{
		RepaymentTrust: 80, TradingTrust: 80, GovernanceTrust: 80,
		ContributionTrust: 80, VerificationTrust: 80, DisputeTrust: 80,
	}
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": core.StakeCap}

	// base = 1 + 100*40/100 + 80*60/100 = 1 + 40 + 48 = 89
	const want = 89
	if got := votingWeight(addr); got != want {
		t.Fatalf("expected the general votingWeight formula's output to stay exactly %d, got %d - the 60/40 formula must not change for this unrelated feature", want, got)
	}
}

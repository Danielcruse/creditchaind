package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func resetModeratorDisputeGovernanceTestState() {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()
	negotiationStore = core.NewNegotiationStore()
	p2pOfferStore = core.NewP2POfferStore()
	companyStore = core.NewCompanyStore()
	reputationEventStore = core.NewReputationEventStore()
}

func TestApplyGovernance_ModeratorDisputeRejectsFabricatedReference(t *testing.T) {
	resetModeratorDisputeGovernanceTestState()

	moderatorStore.Register("md-fab-moderator", "pub", 1000)

	tx := core.Transaction{
		ID:   "md-fab-create",
		From: "md-fab-user",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Dispute a fabricated trade","kind":"MODERATOR_DISPUTE","disputeModeratorAddress":"md-fab-moderator","disputeReferenceType":"negotiation","disputeReferenceId":"never-existed"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created against a fabricated reference ID, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_ModeratorDisputeRejectsWrongModerator(t *testing.T) {
	resetModeratorDisputeGovernanceTestState()

	moderatorStore.Register("md-wrong-moderator", "pub", 1000)
	moderatorStore.Register("md-real-moderator", "pub", 1000)
	negotiationStore.Create(&core.Negotiation{ID: "md-wrong-neg", ModeratorAddress: "md-real-moderator"})

	tx := core.Transaction{
		ID:   "md-wrong-create",
		From: "md-wrong-user",
		Type: core.TxGovernance,
		// Targets a real moderator, but one who was NOT actually
		// assigned to this negotiation - must be rejected just like a
		// fully fabricated reference.
		Data: `{"action":"create","title":"Dispute the wrong moderator","kind":"MODERATOR_DISPUTE","disputeModeratorAddress":"md-wrong-moderator","disputeReferenceType":"negotiation","disputeReferenceId":"md-wrong-neg"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created against a moderator who wasn't actually assigned to the cited record, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyGovernance_ModeratorDisputeAcceptsRealReference(t *testing.T) {
	resetModeratorDisputeGovernanceTestState()

	moderatorStore.Register("md-real-moderator-2", "pub", 1000)
	negotiationStore.Create(&core.Negotiation{ID: "md-real-neg", ModeratorAddress: "md-real-moderator-2"})

	tx := core.Transaction{
		ID:   "md-real-create",
		From: "md-real-user",
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Dispute a real decision","kind":"MODERATOR_DISPUTE","disputeModeratorAddress":"md-real-moderator-2","disputeReferenceType":"negotiation","disputeReferenceId":"md-real-neg"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 1 {
		t.Fatalf("expected exactly 1 proposal created against a genuinely-referenced moderator decision, got %d", len(proposalStore.Proposals))
	}
	for _, p := range proposalStore.Proposals {
		if p.Kind != core.ProposalKindModeratorDispute || p.DisputeModeratorAddress != "md-real-moderator-2" || p.DisputeReferenceID != "md-real-neg" {
			t.Fatalf("unexpected proposal shape: %+v", p)
		}
	}
}

func TestFinalizeProposal_ModeratorDisputePassedAppliesPenaltyOnlyOnPass(t *testing.T) {
	resetModeratorDisputeGovernanceTestState()

	moderatorStore.Register("md-penalty-moderator", "pub", 1000)
	walletStore.Get("md-penalty-moderator").Reputation = 80

	passing := &core.Proposal{
		ID: "md-prop-pass", CreatedBy: "voter-1",
		Kind: core.ProposalKindModeratorDispute, DisputeModeratorAddress: "md-penalty-moderator",
		DisputeReferenceType: "negotiation", DisputeReferenceID: "some-neg",
		Open: true, Quorum: 2, Deadline: 1000,
	}
	proposalStore.Create(passing)
	proposalStore.Voted[passing.ID] = map[string]bool{"voter-1": true, "voter-2": true}
	passing.YesVotes = 2

	finalizeProposal(passing, 1000)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("expected PASSED, got %q", passing.Status)
	}
	if got := walletStore.Get("md-penalty-moderator").Reputation; got != 80-ModeratorDisputeReputationPenalty {
		t.Fatalf("expected moderator reputation reduced by %d, got %d", ModeratorDisputeReputationPenalty, got)
	}

	// A FAILED dispute (quorum not met) must never touch the moderator.
	moderatorStore.Register("md-penalty-moderator-2", "pub", 1000)
	walletStore.Get("md-penalty-moderator-2").Reputation = 80

	failing := &core.Proposal{
		ID: "md-prop-fail", CreatedBy: "voter-1",
		Kind: core.ProposalKindModeratorDispute, DisputeModeratorAddress: "md-penalty-moderator-2",
		DisputeReferenceType: "negotiation", DisputeReferenceID: "some-other-neg",
		Open: true, Quorum: 500, Deadline: 1000,
	}
	proposalStore.Create(failing)
	proposalStore.Voted[failing.ID] = map[string]bool{"voter-1": true}
	failing.YesVotes = 1

	finalizeProposal(failing, 1000)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("expected FAILED (quorum not met), got %q", failing.Status)
	}
	if got := walletStore.Get("md-penalty-moderator-2").Reputation; got != 80 {
		t.Fatalf("expected untouched reputation after a FAILED dispute, got %d", got)
	}
}

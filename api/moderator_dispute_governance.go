package api

import "github.com/creditchain/creditchaind/core"

// moderatorDisputeReferenceNegotiation/Offer/Company are the three
// record types that ever carry a ModeratorAddress - the only kinds of
// past moderator decision a MODERATOR_DISPUTE proposal can cite.
const (
	moderatorDisputeReferenceNegotiation = "negotiation"
	moderatorDisputeReferenceOffer       = "offer"
	moderatorDisputeReferenceCompany     = "company"
)

func isValidModeratorDisputeReferenceType(t string) bool {
	switch t {
	case moderatorDisputeReferenceNegotiation, moderatorDisputeReferenceOffer, moderatorDisputeReferenceCompany:
		return true
	default:
		return false
	}
}

// moderatorDisputeReferenceValid confirms referenceID actually names a
// real record that moderatorAddress was genuinely the assigned
// moderator on - closes the obvious abuse case of citing a fabricated
// or unrelated ID to target an uninvolved moderator with a
// reputation-penalty vote. Re-run both at submission (HandleCreateProposal)
// and at apply time (applyGovernance's "create" case), never trusted
// from one check alone, same discipline every other proposal kind's
// validation already follows.
func moderatorDisputeReferenceValid(referenceType, referenceID, moderatorAddress string) bool {
	if referenceID == "" || moderatorAddress == "" {
		return false
	}
	switch referenceType {
	case moderatorDisputeReferenceNegotiation:
		n, ok := negotiationStore.Get(referenceID)
		return ok && n.ModeratorAddress == moderatorAddress
	case moderatorDisputeReferenceOffer:
		o, ok := p2pOfferStore.Get(referenceID)
		return ok && o.ModeratorAddress == moderatorAddress
	case moderatorDisputeReferenceCompany:
		a, ok := companyStore.Get(referenceID)
		return ok && a.ModeratorAddress == moderatorAddress
	default:
		return false
	}
}

// ModeratorDisputeReputationPenalty is applied to a moderator once a
// community MODERATOR_DISPUTE vote against them genuinely passes
// (real quorum + majority - see finalizeProposal). Same magnitude
// class as ReputationDeltaP2PFraud (30) - both represent a
// system/community-confirmed bad-faith act by a moderator, not a
// minor slip.
const ModeratorDisputeReputationPenalty = 30

// applyModeratorDisputePenalty is called only from finalizeProposal,
// only on a genuine PASSED MODERATOR_DISPUTE outcome. Deliberately
// does not attempt any fund restitution to the disputing user - see
// core.ProposalKindModeratorDispute's doc comment for why that's
// explicit future work, not attempted here.
func applyModeratorDisputePenalty(p *core.Proposal) {
	if p.DisputeModeratorAddress == "" {
		return
	}
	decreaseTrust(walletStore.Get(p.DisputeModeratorAddress), core.DimensionContribution, ModeratorDisputeReputationPenalty, p.Deadline)
	reputationEventStore.Record(&core.ReputationEvent{
		TxID:      p.ID,
		Target:    p.DisputeModeratorAddress,
		Delta:     -int64(ModeratorDisputeReputationPenalty),
		Reason:    "Community vote found this moderator's decision (" + p.DisputeReferenceType + " " + p.DisputeReferenceID + ") to be an abuse of their role",
		Timestamp: p.Deadline,
		Dimension: core.DimensionContribution.String(),
	})
}

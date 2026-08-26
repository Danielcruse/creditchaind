package api

import "github.com/creditchain/creditchaind/core"

// nodeAdmissionStore is the single source of truth for which node
// identities are admitted to the peer gossip network - see
// core.NodeAdmissionStore's own doc comment. Written exclusively by
// applyNodeAdmissionFromProposal below (a passed NODE_ADMISSION
// proposal) and SeedGrandfatheredNodeAdmissions (the one-time cutover
// seed, wired at server startup) - never by any request-handling code
// path directly, which is the entire point: no single party, including
// the operator, can unilaterally admit a new node.
var nodeAdmissionStore = core.NewNodeAdmissionStore()

// grandfatheredNodeAdmissions is the one-time cutover seed for
// core.NodeAdmissionStore - called unconditionally from every node's
// own StartServer (api/server.go), same compiled binary and same list
// on every deployment, per core.NodeAdmissionStore.SeedGrandfatheredNodeAdmissions's
// own doc comment on why this must be code-embedded rather than
// derived from any node's local, non-persisted PeerStore/PEERS config.
//
// Verified live against both real deployments before this was written
// (2026-08-21): node1 (testnet, chainId 1001) and mainnet (chainId 1)
// each currently show peerCount:0 - genuinely no active peer
// connections on either network right now, and neither has a PEERS
// env var configured. Both node identities are included here anyway
// (harmless even across networks - testnet and mainnet are separate
// chains with separate NodeAdmissionStore state despite the shared
// binary, so an entry only ever matters on the network it actually
// belongs to).
//
// node2 (13.40.131.231, testnet) is NOT included - it has been
// unreachable since before this feature was designed and its current
// public key isn't known. If/when it comes back online, it needs
// either a manual addition here (a new deploy) or - better, and a
// real first use of the exact mechanism this file builds - a genuine
// NODE_ADMISSION governance vote once it's reachable again.
var grandfatheredNodeAdmissions = []core.NodeAdmission{
	{PublicKey: "7f11768b950a72d4742e68141678489fa532db414afec90d4eb94aeee73678fb", NodeID: "node-001"},    // node1 (testnet)
	{PublicKey: "4a5eb958c33c35fcb9cccf4381dba4cb488b2e7c8390a03a0a14dfd0bfd5f45a", NodeID: "mainnet-001"}, // mainnet
}

// applyNodeAdmissionFromProposal performs the actual admission grant
// for a PASSED NODE_ADMISSION proposal - the only place a
// NODE_ADMISSION proposal's fields ever take effect, mirroring
// finalizeProposal's existing PARAM_CHANGE/TOKEN_CREATION pattern (see
// api/apply.go and api/token_creation_governance.go).
//
// Re-checks the public key isn't already admitted - the same
// defensive re-check applyTokenCreationFromProposal's own doc comment
// describes (a second proposal for the same identity could pass
// first) - rather than trusting whatever was true when this proposal
// was created. core.NodeAdmissionStore.Grant is itself also
// idempotent, so this check is belt-and-suspenders, not the only
// guard.
func applyNodeAdmissionFromProposal(p *core.Proposal, asOf int64) {
	if p.NodeAdmissionPublicKey == "" {
		return
	}
	if nodeAdmissionStore.IsAdmitted(p.NodeAdmissionPublicKey) {
		return
	}

	nodeAdmissionStore.Grant(&core.NodeAdmission{
		PublicKey:      p.NodeAdmissionPublicKey,
		NodeID:         p.NodeAdmissionNodeID,
		SponsorAddress: p.CreatedBy,
		ProposalID:     p.ID,
		GrantedAt:      asOf,
	})
}

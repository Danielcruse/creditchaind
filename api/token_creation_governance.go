package api

import "github.com/creditchain/creditchaind/core"

// applyTokenCreationFromProposal performs the actual token registration
// for a PASSED TOKEN_CREATION proposal - the only place a
// TOKEN_CREATION proposal's token fields ever take effect, mirroring
// finalizeProposal's existing PARAM_CHANGE/VERSION_ACTIVATION pattern
// (see api/apply.go). Direct self-service registration
// (HandleRegisterToken) is retired - this governance path is the only
// live way a new token gets created going forward.
//
// Re-checks the symbol isn't already registered - a defensive re-check
// against the same kind of submission-to-confirmation race
// applyTokenRegister's own doc comment already describes (e.g. a
// second TOKEN_CREATION proposal for the same symbol passed first) -
// rather than trusting whatever was true back when this proposal was
// created or last voted on.
func applyTokenCreationFromProposal(p *core.Proposal) {
	if _, exists := tokenRegistry.Meta[p.TokenSymbol]; exists {
		return
	}

	standard := p.TokenStandard
	if standard == "" {
		standard = "C1"
	}

	tokenRegistry.Register(p.TokenSymbol, p.TokenName, p.CreatedBy, p.TokenInitialSupply, standard, p.TokenPegReference)
}

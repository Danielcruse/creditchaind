package core

// TokenStandard names one "shape" of token this network understands -
// what mechanism gives it a price, and whether it's a protocol-
// reserved slot or something any wallet can register under. This is
// backend-defined in this pass (not a live governance/admin UI for
// creating new standards) - extensible in code later as new standards
// prove out, per the user's own framing of "later on" growth.
type TokenStandard struct {
	Code           string
	Name           string
	Description    string
	PriceMechanism string
	// System standards are reserved for protocol-defined tokens and can
	// never be selected at custom-token registration time.
	System bool
}

const (
	// PriceMechanismFixed means the token has no market of its own -
	// only CCT itself (C101) works this way.
	PriceMechanismFixed = "FIXED"
	// PriceMechanismStablePegged means the token's pool is deliberately
	// left unseeded/untradeable, same already-existing pattern YEN-e
	// itself uses - it's meant to hold a peg, not float against CCT.
	PriceMechanismStablePegged = "STABLE_PEGGED"
	// PriceMechanismMarketAMM means the token floats against CCT via
	// its liquidity pool once someone deposits into it - this is
	// today's existing, only, custom-token behavior, now named.
	PriceMechanismMarketAMM = "MARKET_AMM"
)

// TokenStandardRegistry is a small, in-memory, code-defined registry -
// not persisted, since its contents are fixed at startup and never
// mutated at runtime (adding a new standard means a code change and a
// redeploy, not an on-chain action, in this pass).
type TokenStandardRegistry struct {
	Standards map[string]*TokenStandard
}

// NewTokenStandardRegistry seeds the three standards this pass defines.
func NewTokenStandardRegistry() *TokenStandardRegistry {
	r := &TokenStandardRegistry{Standards: make(map[string]*TokenStandard)}
	r.Standards["C101"] = &TokenStandard{
		Code:           "C101",
		Name:           "CreditChain Native",
		Description:    "The network's own hub currency (CCT) - reserved, never selectable at token creation.",
		PriceMechanism: PriceMechanismFixed,
		System:         true,
	}
	r.Standards["C117S"] = &TokenStandard{
		Code:           "C117S",
		Name:           "Stable Currency",
		Description:    "A stable/pegged token - its pool starts (and is meant to stay) unseeded, so it doesn't float against CCT like YEN-e.",
		PriceMechanism: PriceMechanismStablePegged,
		System:         false,
	}
	r.Standards["C1"] = &TokenStandard{
		Code:           "C1",
		Name:           "Independent Token",
		Description:    "A token that floats freely against CCT once liquidity is added to its pool - today's default custom-token behavior.",
		PriceMechanism: PriceMechanismMarketAMM,
		System:         false,
	}
	return r
}

// Get returns the standard for code, or nil if unknown.
func (r *TokenStandardRegistry) Get(code string) *TokenStandard {
	return r.Standards[code]
}

// Creatable lists every standard a user can select at registration
// time (System standards excluded), sorted for deterministic output.
func (r *TokenStandardRegistry) Creatable() []*TokenStandard {
	var out []*TokenStandard
	for _, code := range []string{"C1", "C117S"} {
		if s, ok := r.Standards[code]; ok {
			out = append(out, s)
		}
	}
	return out
}

// IsCreatable reports whether code is a real, non-system standard a
// user is allowed to register a token under.
func (r *TokenStandardRegistry) IsCreatable(code string) bool {
	s := r.Standards[code]
	return s != nil && !s.System
}

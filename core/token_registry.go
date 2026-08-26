package core

import "strings"

// CustomToken is a developer-registered token's metadata - the ledger
// itself lives in TokenRegistry.Tokens (a plain TokenStore, same type
// CCT/ETHTEST/USDCTEST/REPT already use), this just records who
// created it and with what starting supply.
type CustomToken struct {
	Symbol        string
	Name          string
	Creator       string
	InitialSupply uint64
	// Standard is the token-standard code (see token_standard.go) this
	// token was registered under - "C1" (independent) or "C117S"
	// (stable). Empty on any token registered before this field
	// existed, treated as "C1" (today's only real behavior at the
	// time), not a migration.
	Standard string
	// PegReference is an optional, purely descriptive string a C117S
	// creator may supply (e.g. "USD") - metadata only, never
	// mechanically enforced (no oracle/mint-controller exists to back
	// a peg for a user-created token).
	PegReference string
}

// StandardOrDefault returns t.Standard, or "C1" if empty (a token
// registered before the standards field existed) - correct as a
// default since C1's MARKET_AMM behavior was the only thing every
// custom token ever did before this field existed.
func (t *CustomToken) StandardOrDefault() string {
	if t.Standard == "" {
		return "C1"
	}
	return t.Standard
}

// TokenRegistry lets any wallet register a new named token using the
// exact same balance/mint/transfer machinery as the built-in ledgers -
// just created dynamically instead of hardcoded at startup. Every
// registered token also gets its own CCT-paired LiquidityPool (see
// liquidity_pool.go): registering the token only creates the ledger
// and mints the creator's initial supply, it does NOT give the token
// a price - that only happens once someone deposits liquidity into
// its pool. This is the deliberate answer to "how is a new token's
// value determined": by what people actually put into the pool
// against it, not by any number the registrant declares.
type TokenRegistry struct {
	Tokens map[string]*TokenStore
	Meta   map[string]*CustomToken
	Pools  map[string]*LiquidityPool
}

func NewTokenRegistry() *TokenRegistry {
	return &TokenRegistry{
		Tokens: make(map[string]*TokenStore),
		Meta:   make(map[string]*CustomToken),
		Pools:  make(map[string]*LiquidityPool),
	}
}

// MaxCustomTokenSupply bounds a single registration's initial mint -
// same "prevent one account from dominating the testnet economy"
// reasoning as MaxTestStakePerAccount.
const MaxCustomTokenSupply = 1_000_000_000

// reservedSymbols can never be registered as a custom token - they're
// the network's own built-in ledgers.
var reservedSymbols = map[string]bool{
	"CCT":      true,
	"ETHTEST":  true,
	"USDCTEST": true,
	"REPT":     true,
	"YENE":     true,
	"ETHB":     true,
	"BTCB":     true,
	"BTCTEST":  true,
}

// ValidateTokenSymbol enforces the same symbol shape a handler needs
// to check before ever calling Register - exported so both
// HandleRegisterToken (fast-fail at submission) and
// applyTokenRegister (re-checked at confirmation time, since another
// registration for the same symbol could have landed in between) use
// one shared rule.
func ValidateTokenSymbol(symbol string) bool {
	if len(symbol) < 2 || len(symbol) > 10 {
		return false
	}
	if reservedSymbols[symbol] {
		return false
	}
	for _, c := range symbol {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// ValidateTokenName is a loose sanity bound, not a security boundary -
// just enough to stop a multi-kilobyte string being stored per token.
func ValidateTokenName(name string) bool {
	name = strings.TrimSpace(name)
	return len(name) > 0 && len(name) <= 64
}

// Register creates symbol's ledger and pool and mints initialSupply
// to creator. Returns false (no-op) if symbol is already taken -
// callers must not treat that as fatal since it can legitimately
// happen at confirmation time even after a clean submission-time
// check (a race between two registrations for the same symbol).
func (r *TokenRegistry) Register(symbol, name, creator string, initialSupply uint64, standard, pegReference string) bool {
	if _, exists := r.Meta[symbol]; exists {
		return false
	}

	store := NewTokenStore()
	store.Token.Name = name
	store.Token.Symbol = symbol
	store.Mint(creator, initialSupply)

	r.Tokens[symbol] = store
	r.Meta[symbol] = &CustomToken{
		Symbol:        symbol,
		Name:          name,
		Creator:       creator,
		InitialSupply: initialSupply,
		Standard:      standard,
		PegReference:  pegReference,
	}
	r.Pools[symbol] = NewLiquidityPool(symbol)

	return true
}

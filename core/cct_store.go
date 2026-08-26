package core

import (
	"math/big"
	"sync"
)

// CctStore is CCT's ledger - a sibling to TokenStore, not a variant of
// it. TokenStore.Balances is a plain map[string]uint64, shared
// polymorphically across every other token in this codebase
// (ETHTEST/USDCTEST/YENE/ETHB/BTCB/REPT/every custom token). CCT alone
// needed 18 decimals of precision at a supply in the billions, and a
// uint64 can hold at most ~18.44 whole CCT at that resolution -
// nowhere near enough. Rather than dragging every other token into
// arbitrary-precision storage it never needed, CCT gets its own store
// with the same method surface (Mint/Transfer/Burn/BalanceOf) so every
// existing call site's shape stays familiar, just backed by
// math/big.Int arithmetic and decimal-string persistence - the same
// "amount as a base-10 string, because *big.Int doesn't round-trip
// through JSON cleanly" convention already used for ETHB's wei amounts
// (see EthCustodialAccount.LastCreditedWei).
type CctStore struct {
	mu sync.Mutex

	// Balances is address -> smallest-unit CCT balance, as a decimal
	// string. An address with no entry has a zero balance.
	Balances map[string]string

	// TotalSupply is the smallest-unit sum of every balance ever
	// minted, minus every amount ever burned - CCT's real circulating
	// supply, checked against MaxTotalCCTSupply by mintCCTCapped.
	TotalSupply string
}

func NewCctStore() *CctStore {
	return &CctStore{
		Balances:    make(map[string]string),
		TotalSupply: "0",
	}
}

// balanceOfLocked reads address's balance as a *big.Int - caller must
// hold s.mu. A missing/empty entry is zero, same as TokenStore's
// zero-value-map-read convention.
func (s *CctStore) balanceOfLocked(address string) *big.Int {
	raw, ok := s.Balances[address]
	if !ok || raw == "" {
		return big.NewInt(0)
	}
	n, err := ParseCctSmallest(raw)
	if err != nil {
		// A corrupted/unparseable stored balance is a bug elsewhere
		// (bad migration, hand-edited state file) - fail safe to zero
		// rather than panic, same "never crash the node over bad
		// persisted data" posture the rest of this codebase takes.
		return big.NewInt(0)
	}
	return n
}

// BalanceOf returns address's current CCT balance as a smallest-unit
// decimal string.
func (s *CctStore) BalanceOf(address string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return FormatCctSmallest(s.balanceOfLocked(address))
}

// Mint credits amount (a smallest-unit decimal string) to address and
// increases TotalSupply by the same amount. Callers that must respect
// MaxTotalCCTSupply go through mintCCTCapped (api/tokenomics.go)
// instead of calling this directly - exactly the same discipline
// TokenStore.Mint's callers already follow.
func (s *CctStore) Mint(address string, amount string) error {
	delta, err := ParseCctSmallest(amount)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	bal := s.balanceOfLocked(address)
	bal.Add(bal, delta)
	s.Balances[address] = FormatCctSmallest(bal)

	supply, _ := ParseCctSmallest(s.TotalSupply)
	if supply == nil {
		supply = big.NewInt(0)
	}
	supply.Add(supply, delta)
	s.TotalSupply = FormatCctSmallest(supply)
	return nil
}

// Transfer moves amount (smallest-unit decimal string) from -> to.
// Returns false on insufficient balance or a malformed amount - same
// fail-closed, no-negative-balance-ever-committed semantics
// TokenStore.Transfer already has, just computed in arbitrary
// precision instead of wrapping-unsafe uint64.
func (s *CctStore) Transfer(from, to, amount string) bool {
	delta, err := ParseCctSmallest(amount)
	if err != nil || delta.Sign() == 0 {
		return err == nil && delta != nil && delta.Sign() == 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fromBal := s.balanceOfLocked(from)
	if fromBal.Cmp(delta) < 0 {
		return false
	}
	fromBal.Sub(fromBal, delta)
	s.Balances[from] = FormatCctSmallest(fromBal)

	toBal := s.balanceOfLocked(to)
	toBal.Add(toBal, delta)
	s.Balances[to] = FormatCctSmallest(toBal)

	return true
}

// Burn is Mint's exact inverse - removes amount from address AND from
// TotalSupply, same semantics/use-case as TokenStore.Burn (self-funded
// loan repayment undoing its own minted-from-thin-air supply; stake
// slashing seizing and destroying CCT rather than redistributing it).
func (s *CctStore) Burn(address string, amount string) bool {
	delta, err := ParseCctSmallest(amount)
	if err != nil {
		return false
	}
	if delta.Sign() == 0 {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bal := s.balanceOfLocked(address)
	if bal.Cmp(delta) < 0 {
		return false
	}
	bal.Sub(bal, delta)
	s.Balances[address] = FormatCctSmallest(bal)

	supply, _ := ParseCctSmallest(s.TotalSupply)
	if supply == nil || supply.Cmp(delta) < 0 {
		supply = delta // defensive: never let TotalSupply go negative
	} else {
		supply.Sub(supply, delta)
	}
	s.TotalSupply = FormatCctSmallest(supply)

	return true
}

// Snapshot returns a copy of the balances map, safe to persist or
// iterate without holding s.mu for the duration - same convention
// several other stores in this codebase (e.g. EthCustodialStore) use.
func (s *CctStore) Snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.Balances))
	for k, v := range s.Balances {
		out[k] = v
	}
	return out
}

// Restore replaces the store's contents wholesale - used only by
// persistence load, mirroring TokenStore's own load-time assignment
// pattern (api/persistence.go just assigns the loaded map directly for
// TokenStore; CctStore needs a method instead since its fields are
// behind a mutex).
func (s *CctStore) Restore(balances map[string]string, totalSupply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if balances == nil {
		balances = make(map[string]string)
	}
	s.Balances = balances
	if totalSupply == "" {
		totalSupply = "0"
	}
	s.TotalSupply = totalSupply
}

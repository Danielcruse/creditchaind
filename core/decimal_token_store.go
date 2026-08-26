package core

import (
	"math/big"
	"sync"
)

// DecimalTokenStore is a decimal-count-parameterized sibling to
// CctStore (core/cct_store.go) - same *big.Int/decimal-string-balance
// shape, same reason (a plain uint64 can't safely hold an 18-decimal
// wei amount without the overflow guard applyBridgeDeposit already
// needs, api/apply.go's `if !delta.IsUint64()`), but usable for any
// decimal count rather than CCT's hardcoded 18. Built standalone rather
// than refactoring CctStore into a thin instantiation of this type -
// CctStore is deeply load-bearing (staking/treasury/order-book/AMM all
// call through ParseCctSmallest/FormatCctSmallest today) and this
// project's own convention is to avoid touching stable, heavily-used
// code when a new, narrower need can be met by a small sibling instead
// (see e.g. why BridgeValidatorStore doesn't extend AdminMultisigStore).
// Sharing CctStore's implementation is a reasonable later cleanup, not
// a blocker for the vault this type exists to support.
type DecimalTokenStore struct {
	mu sync.Mutex

	Decimals int

	// Balances is address -> smallest-unit balance, as a decimal
	// string - same "big.Int doesn't round-trip through JSON cleanly"
	// convention CctStore already established.
	Balances map[string]string

	TotalSupply string
}

func NewDecimalTokenStore(decimals int) *DecimalTokenStore {
	return &DecimalTokenStore{
		Decimals:    decimals,
		Balances:    make(map[string]string),
		TotalSupply: "0",
	}
}

// ParseDecimalSmallest/FormatDecimalSmallest mirror ParseCctSmallest/
// FormatCctSmallest exactly (core/cct_amount.go) - same validation
// (reject empty-as-error-free-zero, malformed, negative), same
// canonical-string output. Kept as package-level functions (not
// methods) since they don't depend on a store's decimal count - the
// decimal count only matters for native-unit<->whole-unit conversions
// external callers do, never for the store's own smallest-unit
// arithmetic.
func ParseDecimalSmallest(s string) (*big.Int, error) {
	if s == "" {
		return big.NewInt(0), nil
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, errCctAmountFormat
	}
	if n.Sign() < 0 {
		return nil, errCctAmountNegative
	}
	return n, nil
}

func FormatDecimalSmallest(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}

func (s *DecimalTokenStore) balanceOfLocked(address string) *big.Int {
	raw, ok := s.Balances[address]
	if !ok || raw == "" {
		return big.NewInt(0)
	}
	n, err := ParseDecimalSmallest(raw)
	if err != nil {
		// A corrupted/unparseable stored balance is a bug elsewhere
		// (bad migration, hand-edited state file) - fail safe to zero
		// rather than panic, same posture CctStore already takes.
		return big.NewInt(0)
	}
	return n
}

func (s *DecimalTokenStore) BalanceOf(address string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return FormatDecimalSmallest(s.balanceOfLocked(address))
}

func (s *DecimalTokenStore) Mint(address string, amount string) error {
	delta, err := ParseDecimalSmallest(amount)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	bal := s.balanceOfLocked(address)
	bal.Add(bal, delta)
	s.Balances[address] = FormatDecimalSmallest(bal)

	supply, _ := ParseDecimalSmallest(s.TotalSupply)
	if supply == nil {
		supply = big.NewInt(0)
	}
	supply.Add(supply, delta)
	s.TotalSupply = FormatDecimalSmallest(supply)
	return nil
}

func (s *DecimalTokenStore) Transfer(from, to, amount string) bool {
	delta, err := ParseDecimalSmallest(amount)
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
	s.Balances[from] = FormatDecimalSmallest(fromBal)

	toBal := s.balanceOfLocked(to)
	toBal.Add(toBal, delta)
	s.Balances[to] = FormatDecimalSmallest(toBal)

	return true
}

func (s *DecimalTokenStore) Burn(address string, amount string) bool {
	delta, err := ParseDecimalSmallest(amount)
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
	s.Balances[address] = FormatDecimalSmallest(bal)

	supply, _ := ParseDecimalSmallest(s.TotalSupply)
	if supply == nil || supply.Cmp(delta) < 0 {
		supply = delta // defensive: never let TotalSupply go negative
	} else {
		supply.Sub(supply, delta)
	}
	s.TotalSupply = FormatDecimalSmallest(supply)

	return true
}

func (s *DecimalTokenStore) Snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.Balances))
	for k, v := range s.Balances {
		out[k] = v
	}
	return out
}

func (s *DecimalTokenStore) Restore(balances map[string]string, totalSupply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if balances != nil {
		s.Balances = balances
	}
	if totalSupply != "" {
		s.TotalSupply = totalSupply
	}
}

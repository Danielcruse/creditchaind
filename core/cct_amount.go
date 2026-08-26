package core

import "math/big"

// CctDecimals is CCT's fixed decimal precision (18, matching ETH/wei) -
// see core/cct_store.go's doc comment for why CCT specifically needed
// this and no other token in this codebase did.
const CctDecimals = 18

// cctSmallestPerWhole is 10^18, parsed once at init rather than
// recomputed per call.
var cctSmallestPerWhole = new(big.Int).Exp(big.NewInt(10), big.NewInt(CctDecimals), nil)

// ParseCctSmallest parses a decimal string of CCT smallest units (the
// same "amount already in its smallest unit, as a base-10 string"
// convention this codebase already uses for ETHB wei - see
// EthCustodialAccount.LastCreditedWei's doc comment for why a raw
// *big.Int isn't used directly: it doesn't survive JSON round-trips
// cleanly on its own). Rejects empty/malformed/negative input - every
// CCT amount in this ledger must be a valid, non-negative integer,
// same invariant TokenStore's uint64 balances already enforced by
// construction.
func ParseCctSmallest(s string) (*big.Int, error) {
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

// FormatCctSmallest renders n as a canonical decimal string (no
// leading zeros, "0" for zero) - the inverse of ParseCctSmallest, and
// what every CctStore method stores back into its Balances map.
func FormatCctSmallest(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}

// CctWholeToSmallest converts a whole-CCT amount (as used throughout
// this codebase before the 18-decimal migration - block reward
// constants, genesis pool amounts, the old MaxTotalCCTSupply, etc.)
// into its smallest-unit equivalent. Used only for constants/migration
// math, never for parsing untrusted input (use ParseCctSmallest for
// that).
func CctWholeToSmallest(whole uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(whole), cctSmallestPerWhole)
}

// CctSmallestToWholeFloor is CctWholeToSmallest's inverse, rounding
// down - used to feed whole-CCT-only fields/displays (Wallet.Rewards,
// PoSR's consensus weight math via wholeCctFloor in
// core/staking_sync.go) from a precise smallest-unit amount. Never
// used for anything that pays out real value - those always move the
// precise smallest-unit string directly.
func CctSmallestToWholeFloor(smallest string) uint64 {
	n, err := ParseCctSmallest(smallest)
	if err != nil {
		return 0
	}
	whole := new(big.Int).Div(n, cctSmallestPerWhole)
	if !whole.IsUint64() {
		return ^uint64(0)
	}
	return whole.Uint64()
}

// CctSmallestToDecimalString renders a smallest-unit amount as a
// human-readable decimal CCT string (e.g. "0.3"), trimming trailing
// fractional zeros and the decimal point entirely for a whole number -
// for DISPLAY only (a fee preview, a UI label), never for a value that
// itself gets parsed back and moved: those always stay in the precise
// smallest-unit string form ParseCctSmallest/FormatCctSmallest handle.
func CctSmallestToDecimalString(smallest *big.Int) string {
	if smallest == nil || smallest.Sign() == 0 {
		return "0"
	}
	whole := new(big.Int)
	frac := new(big.Int)
	whole.DivMod(smallest, cctSmallestPerWhole, frac)
	if frac.Sign() == 0 {
		return whole.String()
	}
	// Left-pad the fractional part to exactly CctDecimals digits, then
	// trim trailing zeros so e.g. 300000000000000000 (0.3 x 10^18)
	// prints as "0.3", not "0.300000000000000000".
	fracStr := frac.String()
	for len(fracStr) < CctDecimals {
		fracStr = "0" + fracStr
	}
	i := len(fracStr)
	for i > 0 && fracStr[i-1] == '0' {
		i--
	}
	return whole.String() + "." + fracStr[:i]
}

type cctAmountError string

func (e cctAmountError) Error() string { return string(e) }

const (
	errCctAmountFormat   cctAmountError = "invalid CCT amount"
	errCctAmountNegative cctAmountError = "CCT amount cannot be negative"
)

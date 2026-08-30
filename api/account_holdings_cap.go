package api

import (
	"encoding/hex"
	"math/big"

	"github.com/creditchain/creditchaind/core"
)

// This file replaces the earlier per-transaction bridge deposit cap
// (BridgeDepositCapWeiETH/BridgeDepositCapSatsBTC) with a per-account
// TOTAL holdings cap, expressed in USD, on the two bridged/custodied
// balances (BTCB + ETHB) - the two assets CreditChain actually holds
// real external value against. Self-custodial ETH/BTC/USDC never touch
// this cap: CreditChain never holds those funds (the private key never
// leaves the user's browser), so there's nothing here for a holdings
// cap to meaningfully limit.
//
// Determinism note (the same constraint the old per-tx cap was built
// to respect): every node must reach the identical accept/partial-
// credit decision for the same confirmed deposit report, so this can
// never read api/price_oracle.go's locally-polled, per-node live price
// cache - two nodes polling at slightly different moments could
// disagree about whether a given deposit crosses the cap. Instead, the
// USD-per-native-unit rates are admin-settable governance params (see
// api/admin_params.go's allowlist), replicated identically to every
// node the same way BridgeDepositCapWeiETH's admin-tunable value
// always was - the tradeoff, same as that earlier mechanism, is that
// the admin is responsible for periodically refreshing these rates as
// the real market price moves; this is a disclosed operational
// responsibility, not a live-oracle-driven guarantee.

// weiPerEthBig / satsPerBtcBig are the fixed native-unit-per-whole-coin
// scales used to convert wei/sats into USD cents against an admin-set
// USD-cents-per-whole-coin rate.
var weiPerEthBig = new(big.Int).SetUint64(1_000_000_000_000_000_000)

const satsPerBtc = 100_000_000

// DefaultAccountHoldingsCapUsdCents is the default per-account total
// BTCB+ETHB holdings cap - $50,000, expressed in cents so every
// comparison here stays integer/deterministic. Admin-settable via
// "AccountHoldingsCapUsdCents" (see adminSettableParams) if this
// figure ever needs to change without a redeploy - this constant is
// only what a node falls back to before that governance param has
// ever been explicitly set (see accountHoldingsRatesSet's "0 =
// unenforced" sibling logic just below); on a node where an admin
// already set a value live, changing this constant alone has no
// effect until they submit a new value.
const DefaultAccountHoldingsCapUsdCents = 5_000_000

// weiToUsdCents / satsToUsdCents convert a native-unit amount to USD
// cents given an admin-set USD-cents-per-whole-coin rate, in
// arbitrary-precision arithmetic (wei amounts can be up to ~1e19, and
// multiplying by a cents rate before dividing needs headroom a plain
// uint64 multiply doesn't safely have).
func weiToUsdCents(wei uint64, centsPerEth uint64) *big.Int {
	v := new(big.Int).Mul(new(big.Int).SetUint64(wei), new(big.Int).SetUint64(centsPerEth))
	return v.Div(v, weiPerEthBig)
}

func satsToUsdCents(sats uint64, centsPerBtc uint64) *big.Int {
	v := new(big.Int).Mul(new(big.Int).SetUint64(sats), new(big.Int).SetUint64(centsPerBtc))
	return v.Div(v, big.NewInt(satsPerBtc))
}

// usdCentsToWei / usdCentsToSats are the inverse conversion, used to
// figure out exactly how much of an over-cap deposit CAN be credited
// (the headroom remaining under the cap, expressed back in native
// units) - same "credit up to the cap, track the rest as excess"
// partial-credit UX the old per-tx cap already established.
func usdCentsToWei(cents *big.Int, centsPerEth uint64) uint64 {
	if cents.Sign() <= 0 || centsPerEth == 0 {
		return 0
	}
	v := new(big.Int).Mul(cents, weiPerEthBig)
	v.Div(v, new(big.Int).SetUint64(centsPerEth))
	if !v.IsUint64() {
		return ^uint64(0)
	}
	return v.Uint64()
}

func usdCentsToSats(cents *big.Int, centsPerBtc uint64) uint64 {
	if cents.Sign() <= 0 || centsPerBtc == 0 {
		return 0
	}
	v := new(big.Int).Mul(cents, big.NewInt(satsPerBtc))
	v.Div(v, new(big.Int).SetUint64(centsPerBtc))
	if !v.IsUint64() {
		return ^uint64(0)
	}
	return v.Uint64()
}

// accountHoldingsRatesSet returns the two admin-settable USD rates and
// whether BOTH are currently configured. If either is unset (0, never
// admin-set), the cap can't be evaluated at all - callers skip
// enforcement entirely rather than guessing, matching this codebase's
// existing "0 = unlimited" fail-open convention rather than freezing
// deposits the moment an admin forgets to set a rate.
func accountHoldingsRatesSet() (centsPerEth, centsPerBtc uint64, ok bool) {
	centsPerEth = governanceParamsStore.GetOrDefault("EthUsdCentsPerEth", 0)
	centsPerBtc = governanceParamsStore.GetOrDefault("BtcUsdCentsPerBtc", 0)
	return centsPerEth, centsPerBtc, centsPerEth > 0 && centsPerBtc > 0
}

// currentAccountHoldingsCents returns a CreditChain account's current
// combined BTCB+ETHB value in USD cents, given the account's own public
// key (needed to resolve both sub-addresses).
func currentAccountHoldingsCents(ccAddress, pubKeyHex string, centsPerEth, centsPerBtc uint64) *big.Int {
	pub, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return big.NewInt(0)
	}
	ethbAddr := resolveBalanceAddress(ccAddress, pubKeyHex, "ETHB")
	btcbAddr := core.DeriveTokenAddress(pub, core.BTCBAddressTag)

	total := weiToUsdCents(bridgedEthStore.BalanceOf(ethbAddr), centsPerEth)
	total.Add(total, satsToUsdCents(bridgedBtcStore.BalanceOf(btcbAddr), centsPerBtc))
	return total
}

// accountHoldingsHeadroomCents returns the account's remaining USD-cent
// headroom under the cap (possibly negative/zero if already at or over
// it) and whether the cap can be evaluated at all (both USD rates set).
// Shared by capEthDepositToAccountHoldingsLimit and its BTC mirror
// below.
func accountHoldingsHeadroomCents(ccAddress, pubKeyHex string) (headroom *big.Int, centsPerEth, centsPerBtc uint64, ok bool) {
	centsPerEth, centsPerBtc, ok = accountHoldingsRatesSet()
	if !ok {
		return nil, 0, 0, false
	}
	capCents := big.NewInt(int64(governanceParamsStore.GetOrDefault("AccountHoldingsCapUsdCents", DefaultAccountHoldingsCapUsdCents)))
	current := currentAccountHoldingsCents(ccAddress, pubKeyHex, centsPerEth, centsPerBtc)
	return new(big.Int).Sub(capCents, current), centsPerEth, centsPerBtc, true
}

// capEthDepositToAccountHoldingsLimit is the ETH-side "how much of this
// deposit can actually be credited" decision (see applyBridgeDeposit in
// api/apply.go) - given the full observed wei delta, returns the amount
// to actually credit (capped at the account's remaining USD headroom)
// and the excess (0 if nothing was capped). If the rates aren't both
// set, or the account still has full headroom, the whole delta is
// always allowed through - this never rejects a deposit outright, only
// ever caps how much of it is credited, matching the old per-tx cap's
// own "never drop, only defer" excess-tracking design.
func capEthDepositToAccountHoldingsLimit(ccAddress, pubKeyHex string, deltaWei uint64) (creditWei, excessWei uint64) {
	headroom, centsPerEth, _, ok := accountHoldingsHeadroomCents(ccAddress, pubKeyHex)
	if !ok {
		return deltaWei, 0
	}
	if headroom.Sign() <= 0 {
		return 0, deltaWei
	}
	deltaValue := weiToUsdCents(deltaWei, centsPerEth)
	if deltaValue.Cmp(headroom) <= 0 {
		return deltaWei, 0
	}
	allowed := usdCentsToWei(headroom, centsPerEth)
	if allowed >= deltaWei {
		return deltaWei, 0
	}
	return allowed, deltaWei - allowed
}

// capBtcDepositToAccountHoldingsLimit is the BTC-side mirror of
// capEthDepositToAccountHoldingsLimit above - see its doc comment.
func capBtcDepositToAccountHoldingsLimit(ccAddress, pubKeyHex string, deltaSats uint64) (creditSats, excessSats uint64) {
	headroom, _, centsPerBtc, ok := accountHoldingsHeadroomCents(ccAddress, pubKeyHex)
	if !ok {
		return deltaSats, 0
	}
	if headroom.Sign() <= 0 {
		return 0, deltaSats
	}
	deltaValue := satsToUsdCents(deltaSats, centsPerBtc)
	if deltaValue.Cmp(headroom) <= 0 {
		return deltaSats, 0
	}
	allowed := usdCentsToSats(headroom, centsPerBtc)
	if allowed >= deltaSats {
		return deltaSats, 0
	}
	return allowed, deltaSats - allowed
}

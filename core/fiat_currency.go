package core

import "strings"

// AllowedOffAppCurrencies is the fixed list of real-world settlement
// currencies a verified Trading Hub may post an OFFAPP HubRate in (see
// hub_rate.go's FiatSymbol doc comment) - a hard allowlist, not free
// text, and it deliberately has NO United States Dollar entry at all.
// Facilitating USD-denominated peer-to-peer settlement - even though
// this codebase never touches the actual dollars itself, since OFFAPP
// settlement always happens off-app between the two traders - is money
// transmission under US federal and most state law, and this platform
// holds no MSB/money-transmitter license anywhere. This is a
// compliance boundary, not a UX preference: see IsAllowedOffAppCurrency
// and RejectedOffAppCurrencyReason, and core.CompanyStore.Review's
// identical allowlist-not-blocklist approach for a Trading Hub's own
// jurisdiction (company.go's AllowedHubCountries).
//
// Not to be confused with api/price_oracle.go's supportedCurrencies -
// that is a read-only "show my balance's value in X" DISPLAY
// preference (GET /price/external) that never moves real settlement
// funds between two traders and legitimately includes USD, same as
// any price ticker quoting a dollar value would.
//
// A disclosed judgment call on breadth/ordering, not a locked spec -
// covers the currencies most active in real-world P2P crypto trading
// across Europe, Africa, Asia, the Middle East, Oceania, and the
// Americas (ex-US) without attempting to be an exhaustive ISO 4217
// list.
var AllowedOffAppCurrencies = []string{
	"EUR", "GBP", "CHF", "SEK", "NOK", "DKK", "PLN",
	"NGN", "GHS", "KES", "ZAR", "EGP", "TZS", "UGX", "XOF", "XAF", "RWF", "ETB", "MAD", "DZD", "ZMW",
	"INR", "PKR", "BDT", "PHP", "IDR", "VND", "THB", "MYR", "SGD", "CNY", "KRW",
	"AED", "SAR", "TRY",
	"CAD", "AUD", "NZD", "JPY",
	"BRL", "MXN", "ARS", "CLP", "COP", "PEN",
}

// normalizeCurrencyCode uppercases and trims whitespace before any
// allowlist/alias comparison, so "eur", " EUR ", and "EUR" all match
// identically regardless of how a direct (non-dropdown) API caller
// happened to case or pad it.
func normalizeCurrencyCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// IsAllowedOffAppCurrency reports whether code is one of
// AllowedOffAppCurrencies - the actual server-side enforcement behind
// the marketplace's currency dropdown (see api/hub_rate.go's
// HandleSetHubRate/applyHubRateSet), so a caller that bypasses the
// frontend and hits the API directly still can't post a rate in an
// unsupported currency.
func IsAllowedOffAppCurrency(code string) bool {
	code = normalizeCurrencyCode(code)
	for _, c := range AllowedOffAppCurrencies {
		if c == code {
			return true
		}
	}
	return false
}

// usdAliases is every common way "US Dollar" might get typed into a
// free-text field - checked ahead of the plain allowlist rejection so
// a USD attempt gets an error naming the real reason (US money-
// transmission law), not a generic "unsupported currency" message
// indistinguishable from an ordinary typo.
var usdAliases = map[string]bool{
	"USD": true, "US$": true, "$": true, "USDOLLAR": true, "USDOLLARS": true,
	"DOLLAR": true, "DOLLARS": true, "US DOLLAR": true, "US DOLLARS": true,
	"U.S. DOLLAR": true, "UNITED STATES DOLLAR": true, "UNITEDSTATESDOLLAR": true,
}

// IsUSDCurrency reports whether code is USD or one of its common
// aliases (usdAliases above).
func IsUSDCurrency(code string) bool {
	return usdAliases[normalizeCurrencyCode(code)]
}

// RejectedOffAppCurrencyReason returns the specific, honest reason an
// OFFAPP FiatSymbol is being rejected ("" if it's actually allowed) -
// shared by the HTTP fast-fail handler and the block-confirmation
// apply function so the two can never give a caller two different
// explanations for the same rejection.
func RejectedOffAppCurrencyReason(code string) string {
	if IsUSDCurrency(code) {
		return "USD is not a supported off-app settlement currency on this marketplace - facilitating USD peer-to-peer settlement is money transmission under US law, which this platform is not licensed for anywhere"
	}
	if !IsAllowedOffAppCurrency(code) {
		return "fiatSymbol must be one of the supported off-app currencies (see GET /hub-rates/currencies)"
	}
	return ""
}

package core

import "strings"

// AllowedHubCountries is the fixed list of countries a Trading Hub
// applicant may declare as its own jurisdiction
// (CompanyApplication.Country) - a hard allowlist, not free text, and
// it deliberately has NO United States entry at all. Rather than
// trying to catch every way "US"/"USA"/"United States"/a US state name
// could be typed into a free-text field, a hub simply has no way to
// select it in the first place - the same allowlist-over-blocklist
// approach as AllowedOffAppCurrencies (fiat_currency.go), for the
// identical underlying reason: this platform holds no US money-
// transmitter/MSB license anywhere, and a US-based Trading Hub
// facilitating real-world settlement is a genuine regulatory exposure
// - fines, and possible personal legal consequences for whichever
// moderator approved it. CompanyStore.Review (below) is the actual
// enforcement point: it refuses to approve ANY application whose
// Country isn't on this list, which includes both "left blank" and
// "somehow set to something not on this list at all" (e.g. a direct,
// pre-this-feature API caller).
//
// A disclosed judgment call on breadth, not a locked spec or an
// exhaustive ISO 3166 list - ~100 countries across every populated
// continent, deliberately excluding both the United States itself and
// its unincorporated territories (Puerto Rico, Guam, the US Virgin
// Islands, American Samoa, the Northern Mariana Islands), which fall
// under the exact same US money-transmission jurisdiction as the
// mainland for this purpose.
var AllowedHubCountries = []string{
	"Albania", "Algeria", "Angola", "Argentina", "Armenia", "Australia", "Austria", "Azerbaijan",
	"Bahrain", "Bangladesh", "Belgium", "Belize", "Benin", "Bolivia", "Bosnia and Herzegovina", "Botswana", "Brazil", "Bulgaria", "Burkina Faso",
	"Cambodia", "Cameroon", "Canada", "Chile", "China", "Colombia", "Costa Rica", "Croatia", "Cyprus", "Czechia",
	"Denmark", "Dominican Republic",
	"Ecuador", "Egypt", "El Salvador", "Estonia", "Eswatini", "Ethiopia",
	"Fiji", "Finland", "France",
	"Gabon", "Georgia", "Germany", "Ghana", "Greece", "Guatemala", "Guyana",
	"Honduras", "Hong Kong", "Hungary",
	"Iceland", "India", "Indonesia", "Ireland", "Israel", "Italy", "Ivory Coast",
	"Jamaica", "Japan", "Jordan",
	"Kazakhstan", "Kenya", "Kuwait",
	"Laos", "Latvia", "Lebanon", "Lesotho", "Liberia", "Lithuania", "Luxembourg",
	"Madagascar", "Malawi", "Malaysia", "Maldives", "Mali", "Malta", "Mauritius", "Mexico", "Moldova", "Mongolia", "Montenegro", "Morocco", "Mozambique",
	"Namibia", "Nepal", "Netherlands", "New Zealand", "Nicaragua", "Niger", "Nigeria", "North Macedonia", "Norway",
	"Oman",
	"Pakistan", "Panama", "Papua New Guinea", "Paraguay", "Peru", "Philippines", "Poland", "Portugal",
	"Qatar",
	"Romania", "Rwanda",
	"Saudi Arabia", "Senegal", "Serbia", "Sierra Leone", "Singapore", "Slovakia", "Slovenia", "South Africa", "South Korea", "Spain", "Sri Lanka", "Sweden", "Switzerland",
	"Taiwan", "Tanzania", "Thailand", "Togo", "Trinidad and Tobago", "Tunisia", "Turkey",
	"Uganda", "Ukraine", "United Arab Emirates", "United Kingdom", "Uruguay", "Uzbekistan",
	"Vietnam",
	"Zambia", "Zimbabwe",
}

func normalizeCountryName(country string) string {
	return strings.TrimSpace(country)
}

// IsAllowedHubCountry reports whether country is one of
// AllowedHubCountries - case-sensitive on purpose (this list backs a
// dropdown, not a free-text field a human might re-case; an exact-
// string match is simpler to reason about than a case-insensitive one
// and can't be gamed by unusual casing the way IsAllowedOffAppCurrency
// guards against for currency codes).
func IsAllowedHubCountry(country string) bool {
	country = normalizeCountryName(country)
	for _, c := range AllowedHubCountries {
		if c == country {
			return true
		}
	}
	return false
}

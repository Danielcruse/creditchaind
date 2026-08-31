package core

// SponsoredPostCategory is a fixed allowlist for a Sponsored Post's
// subject matter - a dropdown selection, never free text, same
// "allowlist not free text" reasoning as AllowedOffAppCurrencies
// (fiat_currency.go) and AllowedHubCountries (company_country.go): a
// category decides which review lane a post goes through (see
// MakesNumericClaims below), so it can't be typo'd or omitted the way
// a free-text field could be.
type SponsoredPostCategory string

const (
	// CategoryValidatorStaking/CategoryLending/CategoryTradingHub all
	// commonly carry a quantifiable financial claim (an APR, a rate, a
	// yield) - MakesNumericClaims is true for these three, routing them
	// through the moderator's numeric-claims review lane (see
	// api/sponsored_post.go's doc comment on that lane).
	CategoryValidatorStaking SponsoredPostCategory = "VALIDATOR_STAKING"
	CategoryLending          SponsoredPostCategory = "LENDING"
	CategoryTradingHub       SponsoredPostCategory = "TRADING_HUB"
	// CategoryGeneralBusiness/CategoryCommunity are ordinary content -
	// a shop, a service, a community announcement - with no financial
	// claim to fact-check. Ordinary moderation only (no scam/
	// impersonation), same as any other moderator review in this
	// codebase.
	CategoryGeneralBusiness SponsoredPostCategory = "GENERAL_BUSINESS"
	CategoryCommunity       SponsoredPostCategory = "COMMUNITY"
)

// AllowedSponsoredPostCategories is the full allowlist, in a stable
// display order - for anywhere that needs to *list* the categories
// (e.g. the advertiser submission form's dropdown) rather than just
// validate one.
var AllowedSponsoredPostCategories = []SponsoredPostCategory{
	CategoryValidatorStaking,
	CategoryLending,
	CategoryTradingHub,
	CategoryGeneralBusiness,
	CategoryCommunity,
}

// IsAllowedSponsoredPostCategory reports whether category is one of
// AllowedSponsoredPostCategories.
func IsAllowedSponsoredPostCategory(category SponsoredPostCategory) bool {
	for _, c := range AllowedSponsoredPostCategories {
		if c == category {
			return true
		}
	}
	return false
}

// MakesNumericClaims reports whether category is one that commonly
// carries a quantifiable financial claim - gates whether a moderator's
// review UI shows the claimed-numbers-vs-real-numbers panel (see
// api/sponsored_post.go). A category outside AllowedSponsoredPostCategories
// (should never happen post-validation, but this function makes no
// assumption) is treated as false - the stricter lane only ever
// applies to a category explicitly known to need it.
func MakesNumericClaims(category SponsoredPostCategory) bool {
	switch category {
	case CategoryValidatorStaking, CategoryLending, CategoryTradingHub:
		return true
	default:
		return false
	}
}

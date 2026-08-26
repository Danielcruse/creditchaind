package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/creditchain/creditchaind/core"
)

type hubDirectoryFilterEntry struct {
	Hub              string   `json:"hub"`
	Currencies       []string `json:"currencies"`
	ServiceProviders []string `json:"serviceProviders"`
}

// HandleListHubDirectoryFilters is what powers the marketplace
// directory's currency + service-provider filter bar - for every
// verified Trading Hub, the distinct off-app currencies it has an
// active OFFAPP rate posted for (ONCHAIN rates are token-for-token,
// not a real-world payment method a filter like "PayPal" makes sense
// against, so excluded here), plus its own hub-authored list of
// accepted payment providers (see core.HubRules.ServiceProviders).
// Computed fresh on every call, the same read-time aggregation over
// already-existing state HandleListHubRates already does for its own
// per-rate live-balance enrichment - no new persisted state, nothing
// to keep in sync.
func HandleListHubDirectoryFilters(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	currenciesByHub := map[string]map[string]bool{}
	for _, rate := range hubRateStore.List() {
		if !rate.Active || rate.IsOnChain() {
			continue
		}
		if currenciesByHub[rate.Hub] == nil {
			currenciesByHub[rate.Hub] = map[string]bool{}
		}
		currenciesByHub[rate.Hub][rate.FiatSymbol] = true
	}

	out := make([]hubDirectoryFilterEntry, 0)
	for _, company := range companyStore.List() {
		if company.Status != core.CompanyVerified {
			continue
		}

		var currencies []string
		for c := range currenciesByHub[company.Address] {
			currencies = append(currencies, c)
		}
		sort.Strings(currencies)

		var services []string
		if rules, ok := hubRulesStore.Get(company.Address); ok {
			services = rules.ServiceProviders
		}

		out = append(out, hubDirectoryFilterEntry{
			Hub:              company.Address,
			Currencies:       currencies,
			ServiceProviders: services,
		})
	}

	json.NewEncoder(w).Encode(out)
}

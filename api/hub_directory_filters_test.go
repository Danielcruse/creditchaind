package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestListHubDirectoryFilters_AggregatesCurrenciesAndServiceProviders(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Name:      "Test Hub",
		Status:    core.CompanyVerified,
	})

	rulesTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRulesSet, hubRulesSetData{
		ServiceProviders: []string{"Bank Transfer", "PayPal"},
	}, 1000)
	applyTxOnce(rulesTx)

	// Two active OFFAPP rates (NGN, USD) plus one inactive rate and one
	// ONCHAIN rate - only the two active OFFAPP currencies should surface.
	ngnRateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "NGN", SellRate: 1000, Active: true,
	}, 1001)
	applyTxOnce(ngnRateTx)
	usdRateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "REPT", FiatSymbol: "USD", SellRate: 500, Active: true,
	}, 1002)
	applyTxOnce(usdRateTx)
	inactiveRateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "EUR", SellRate: 700, Active: false,
	}, 1003)
	applyTxOnce(inactiveRateTx)
	onChainRateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "REPT", Mode: "ONCHAIN", SellRate: 300, Active: true,
	}, 1004)
	applyTxOnce(onChainRateTx)

	req := httptest.NewRequest(http.MethodGet, "/hub-directory-filters", nil)
	rec := httptest.NewRecorder()
	HandleListHubDirectoryFilters(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var entries []hubDirectoryFilterEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 verified hub entry, got %d: %+v", len(entries), entries)
	}

	entry := entries[0]
	if entry.Hub != hubAddr {
		t.Fatalf("expected hub %s, got %s", hubAddr, entry.Hub)
	}
	wantCurrencies := map[string]bool{"NGN": true, "USD": true}
	if len(entry.Currencies) != len(wantCurrencies) {
		t.Fatalf("expected currencies %v, got %v", wantCurrencies, entry.Currencies)
	}
	for _, c := range entry.Currencies {
		if !wantCurrencies[c] {
			t.Fatalf("unexpected currency %q in %v - EUR (inactive) and REPT (onchain) must not appear", c, entry.Currencies)
		}
	}

	wantServices := map[string]bool{"Bank Transfer": true, "PayPal": true}
	if len(entry.ServiceProviders) != len(wantServices) {
		t.Fatalf("expected service providers %v, got %v", wantServices, entry.ServiceProviders)
	}
	for _, s := range entry.ServiceProviders {
		if !wantServices[s] {
			t.Fatalf("unexpected service provider %q in %v", s, entry.ServiceProviders)
		}
	}
}

func TestListHubDirectoryFilters_UnverifiedHubExcluded(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, _, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address: hubAddr,
		Status:  core.CompanyPending,
	})

	req := httptest.NewRequest(http.MethodGet, "/hub-directory-filters", nil)
	rec := httptest.NewRecorder()
	HandleListHubDirectoryFilters(rec, req)

	var entries []hubDirectoryFilterEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected an unverified hub to be excluded, got %+v", entries)
	}
}

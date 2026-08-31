package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleSetHubRate_RejectsUSD confirms the marketplace's actual
// server-side enforcement: a verified Trading Hub cannot post an OFFAPP
// rate in USD, even by calling the API directly (bypassing the
// frontend's dropdown entirely) - see core.AllowedOffAppCurrencies' doc
// comment for why.
func TestHandleSetHubRate_RejectsUSD(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Status:    core.CompanyVerified,
	})

	body := buildSignedEnvelopeJSON(t, hubPub, hubPriv, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "USD", SellRate: 1000, Active: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/hub-rates/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleSetHubRate(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 rejecting USD, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := hubRateStore.Get(hubAddr, "CCT", "", "USD"); ok {
		t.Fatal("expected no rate to have been stored for a rejected USD submission")
	}
}

// TestHandleSetHubRate_RejectsUnsupportedCurrency confirms this isn't a
// USD-specific blocklist but a real allowlist - a currency that isn't
// USD and also isn't on the list is rejected too.
func TestHandleSetHubRate_RejectsUnsupportedCurrency(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Status:    core.CompanyVerified,
	})

	body := buildSignedEnvelopeJSON(t, hubPub, hubPriv, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "ZZZ", SellRate: 1000, Active: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/hub-rates/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleSetHubRate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting an unsupported currency code, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleSetHubRate_AllowsSupportedOffAppCurrency is the control
// case - an allowed currency still works exactly as before.
func TestHandleSetHubRate_AllowsSupportedOffAppCurrency(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Status:    core.CompanyVerified,
	})

	body := buildSignedEnvelopeJSON(t, hubPub, hubPriv, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "NGN", SellRate: 1000, Active: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/hub-rates/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleSetHubRate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an allowed currency, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestApplyHubRateSet_RejectsUSDAtConfirmationToo confirms the same
// rejection holds at block-confirmation time (applyHubRateSet), not
// just the HTTP fast-fail - never trusting submission-time state
// alone, this codebase's usual discipline.
func TestApplyHubRateSet_RejectsUSDAtConfirmationToo(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Status:    core.CompanyVerified,
	})

	tx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "USD", SellRate: 1000, Active: true,
	}, 1000)
	applyTxOnce(tx)

	if _, ok := hubRateStore.Get(hubAddr, "CCT", "", "USD"); ok {
		t.Fatal("expected no rate to have been stored for a USD confirmation")
	}
}

// TestHandleListOffAppCurrencies_ExcludesUSD confirms the endpoint the
// frontend dropdown populates from never includes USD - the frontend
// literally cannot offer it as an option.
func TestHandleListOffAppCurrencies_ExcludesUSD(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/hub-rates/currencies", nil)
	rec := httptest.NewRecorder()
	HandleListOffAppCurrencies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var currencies []string
	if err := json.Unmarshal(rec.Body.Bytes(), &currencies); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(currencies) == 0 {
		t.Fatal("expected a non-empty currency list")
	}
	for _, c := range currencies {
		if c == "USD" {
			t.Fatal("USD must never appear in the served currency list")
		}
	}
}

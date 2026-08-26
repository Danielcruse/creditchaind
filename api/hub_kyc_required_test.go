package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// TestRequestHubTrade_RequireKYCBlocksUntilSubmitted is the actual
// enforcement property core.HubRules.RequireKYC exists for: once a hub
// opts in, HandleRequestHubTrade must refuse an unverified trader's
// trade request outright (never even recorded as a transaction), and
// must allow it through once that same trader has submitted their
// phone/ID to this specific hub (api/hub_kyc.go).
func TestRequestHubTrade_RequireKYCBlocksUntilSubmitted(t *testing.T) {
	resetApplyTestState()
	setHubKYCTestEncryptionKey(t)

	hubPub, _, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, Status: core.CompanyVerified})
	hubRulesStore.Set(hubAddr, "", nil, 0, nil, true, nil, 1000)

	hubRateStore.Set(&core.HubRate{
		Hub:          hubAddr,
		HubPublicKey: hex.EncodeToString(hubPub),
		Symbol:       "CCT",
		FiatSymbol:   "NGN",
		SellRate:     1_000_000, // 1 CCT per 1 NGN, scaled by core.HubRateScale
		Active:       true,
	})
	cctStore.Mint(hubAddr, cctW(1000))

	traderPub, traderPriv, _ := core.GenerateKeyPair()

	tradeReq := hubTradeRequestData{Hub: hubAddr, Symbol: "CCT", Amount: 10, FiatSymbol: "NGN", Direction: "BUY_FROM_HUB"}

	blockedBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, tradeReq)
	blockedReq := httptest.NewRequest(http.MethodPost, "/hub-rate/request-trade", bytes.NewReader(blockedBody))
	blockedRec := httptest.NewRecorder()
	HandleRequestHubTrade(blockedRec, blockedReq)
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 before KYC submitted, got %d: %s", blockedRec.Code, blockedRec.Body.String())
	}

	kycBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubKYCSubmitData{
		Hub: hubAddr, Phone: "+2348012345678", IDType: "passport", IDNumber: "A1234567",
	})
	kycReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/submit", bytes.NewReader(kycBody))
	kycRec := httptest.NewRecorder()
	HandleSubmitHubKYC(kycRec, kycReq)
	if kycRec.Code != http.StatusOK {
		t.Fatalf("expected 200 submitting KYC, got %d: %s", kycRec.Code, kycRec.Body.String())
	}

	allowedBody := buildSignedEnvelopeJSONAt(t, traderPub, traderPriv, tradeReq, time.Now().Unix()+1)
	allowedReq := httptest.NewRequest(http.MethodPost, "/hub-rate/request-trade", bytes.NewReader(allowedBody))
	allowedRec := httptest.NewRecorder()
	HandleRequestHubTrade(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusOK {
		t.Fatalf("expected 200 after KYC submitted, got %d: %s", allowedRec.Code, allowedRec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(allowedRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] == "" {
		t.Fatalf("expected a real trade-request response, got %+v", resp)
	}
}

// TestRequestHubTrade_NoRequireKYCUnaffected proves a hub that never
// opted into RequireKYC keeps working exactly as before this feature -
// no regression for the default (false) case.
func TestRequestHubTrade_NoRequireKYCUnaffected(t *testing.T) {
	resetApplyTestState()

	hubPub, _, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, Status: core.CompanyVerified})

	hubRateStore.Set(&core.HubRate{
		Hub:          hubAddr,
		HubPublicKey: hex.EncodeToString(hubPub),
		Symbol:       "CCT",
		FiatSymbol:   "NGN",
		SellRate:     1_000_000,
		Active:       true,
	})
	cctStore.Mint(hubAddr, cctW(1000))

	traderPub, traderPriv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubTradeRequestData{
		Hub: hubAddr, Symbol: "CCT", Amount: 10, FiatSymbol: "NGN", Direction: "BUY_FROM_HUB",
	})
	req := httptest.NewRequest(http.MethodPost, "/hub-rate/request-trade", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleRequestHubTrade(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a hub that never required KYC, got %d: %s", rec.Code, rec.Body.String())
	}
}

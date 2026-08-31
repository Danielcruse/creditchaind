package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleApplyCompany_RejectsMissingOrUSCountry confirms the HTTP
// fast-fail behind Trading Hub applications - a US jurisdiction (or no
// jurisdiction at all) is refused before an application is even
// created, so an applicant never pays the fee for something that can
// never be approved (see core.CompanyStore.Review's identical hard
// block, which is the real, consensus-level enforcement).
func TestHandleApplyCompany_RejectsMissingOrUSCountry(t *testing.T) {
	tests := []struct {
		name    string
		country string
	}{
		{"missing country", ""},
		{"United States", "United States"},
		{"nonsense country", "Nowhereland"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetHubOperatorTestState()

			pub, priv, _ := core.GenerateKeyPair()
			addr := core.GenerateAddress(pub)
			stakingStore.Stakes[addr] = map[string]uint64{"CCT": HubMinimumStakeCCT}
			cctStore.Mint(addr, cctW(HubApplyFeeCCT))

			body := buildSignedEnvelopeJSON(t, pub, priv, companyApplyData{
				Name: "Test Hub", Country: tt.country,
			})
			req := httptest.NewRequest(http.MethodPost, "/companies/apply", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			HandleApplyCompany(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
			}
			if _, ok := companyStore.Get(addr); ok {
				t.Fatal("expected no application to have been created")
			}
		})
	}
}

// TestHandleApplyCompany_AllowsEligibleCountry is the control case - a
// real, allowed jurisdiction still submits normally.
func TestHandleApplyCompany_AllowsEligibleCountry(t *testing.T) {
	resetHubOperatorTestState()

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": HubMinimumStakeCCT}
	cctStore.Mint(addr, cctW(HubApplyFeeCCT))

	body := buildSignedEnvelopeJSON(t, pub, priv, companyApplyData{
		Name: "Test Hub", Country: "Nigeria",
	})
	req := httptest.NewRequest(http.MethodPost, "/companies/apply", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleApplyCompany(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleReviewCompany_RefusesApprovalForUSApplication confirms the
// HTTP fast-fail on the review side too - even if an application
// somehow reached PENDING with an ineligible Country (e.g. one created
// before this feature existed), a moderator's approval is refused with
// a clear reason rather than silently no-opping.
func TestHandleReviewCompany_RefusesApprovalForUSApplication(t *testing.T) {
	resetHubOperatorTestState()

	modPub, modPriv, _ := core.GenerateKeyPair()
	modAddr := core.GenerateAddress(modPub)
	moderatorStore.Register(modAddr, "mod-pubkey", 1000)

	const hubAddr = "us-hub-under-review"
	companyStore.Apply(&core.CompanyApplication{
		Address:          hubAddr,
		Name:             "US Hub",
		Country:          "United States",
		Status:           core.CompanyPending,
		ModeratorAddress: modAddr,
		CertificateHash:  "real-cert-hash",
	})

	body := buildSignedEnvelopeJSON(t, modPub, modPriv, companyReviewData{Address: hubAddr, Approve: true})
	req := httptest.NewRequest(http.MethodPost, "/companies/review", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleReviewCompany(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	app, _ := companyStore.Get(hubAddr)
	if app.Status != core.CompanyPending {
		t.Fatalf("expected the application to remain PENDING, got %s", app.Status)
	}
}

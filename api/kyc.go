package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// This file gates the peer-lending marketplace (Lend/Marketplace/Your
// Negotiations on the frontend) behind a combined "security pass"
// (current reputation >= core.MinReputationForLending) and basic
// self-attested KYC (name/DOB/country/terms agreement) - see
// core/kyc.go's doc comment for exactly what this is (and isn't) a
// guarantee of. Every mutating lend-related handler checks
// kycStore.IsApproved before recording its transaction - see
// api/loan_offers.go and api/negotiations.go.

var kycStore = core.NewKYCStore()

type kycSubmitData struct {
	FullName    string `json:"fullName"`
	DateOfBirth string `json:"dateOfBirth"`
	Country     string `json:"country"`
	AgreedTerms bool   `json:"agreedTerms"`
}

// HandleKYCSubmit records/replaces the caller's self-attested KYC
// fields as a real block-confirmed transaction (not a node-local
// mutation), so it replicates identically to every node - consistent
// with every other state change in this codebase.
func HandleKYCSubmit(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req kycSubmitData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.FullName) == 0 || len(req.FullName) > 200 {
		http.Error(w, "fullName must be 1-200 characters", http.StatusBadRequest)
		return
	}
	if len(req.DateOfBirth) == 0 || len(req.DateOfBirth) > 20 {
		http.Error(w, "dateOfBirth must be 1-20 characters", http.StatusBadRequest)
		return
	}
	if len(req.Country) == 0 || len(req.Country) > 100 {
		http.Error(w, "country must be 1-100 characters", http.StatusBadRequest)
		return
	}
	if !req.AgreedTerms {
		http.Error(w, "you must agree to the terms to submit", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxKYCSubmit, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type kycStatusResponse struct {
	Submitted     bool   `json:"submitted"`
	Approved      bool   `json:"approved"`
	Reputation    uint64 `json:"reputation"`
	MinReputation uint64 `json:"minReputation"`
	FullName      string `json:"fullName,omitempty"`
	Country       string `json:"country,omitempty"`
}

// requireLendingAccess writes a 403 and returns false if address
// hasn't cleared the combined security-pass + KYC gate - the shared
// check every lend-related mutating handler runs before recordTx (see
// this file's top-of-file doc comment). Server-side, not cosmetic -
// this is what actually blocks the request, independent of whatever
// the frontend chooses to show or hide.
func requireLendingAccess(w http.ResponseWriter, address string) bool {
	// Repayment Trust specifically, not overall TSI - lending access is
	// exactly the credit-risk signal this gate exists to check; a
	// wallet with poor repayment history shouldn't be able to hide
	// behind strong Trading/Governance/etc. standing. See
	// core.TrustDimensionValue's doc comment for why no cutover check
	// is needed at a read site like this.
	reputation := core.TrustDimensionValue(walletStore.Get(address), core.DimensionRepayment)
	if !kycStore.IsApproved(address, reputation) {
		http.Error(
			w,
			"lending marketplace access requires KYC submission and a minimum reputation - see /kyc/status",
			http.StatusForbidden,
		)
		return false
	}
	return true
}

// HandleKYCStatus reports whether address currently clears the
// lending gate - re-derived from current reputation on every call
// (see core.KYCStore.IsApproved), never a stale cached flag.
func HandleKYCStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	rec, submitted := kycStore.Get(address)
	// Same Repayment Trust value the actual gate check
	// (requireLendingAccess) uses - shown here too so this status page
	// never displays a "Reputation" number next to MinReputation that's
	// different from what the gate decision was actually based on.
	reputation := core.TrustDimensionValue(walletStore.Get(address), core.DimensionRepayment)

	resp := kycStatusResponse{
		Submitted:     submitted,
		Approved:      kycStore.IsApproved(address, reputation),
		Reputation:    reputation,
		MinReputation: core.MinReputationForLending,
	}
	if submitted {
		resp.FullName = rec.FullName
		resp.Country = rec.Country
	}

	json.NewEncoder(w).Encode(resp)
}

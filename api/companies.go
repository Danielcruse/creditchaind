package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// This file is the testnet-only "Verified Trading Company" placeholder
// - see core/company.go's doc comment for the scope and its explicit
// limits. Nothing here performs real identity verification, and none
// of it should ever be presented to a user as real KYC/AML compliance.

var companyStore = core.NewCompanyStore()

// HubApplyFeeCCT is a CCT anti-spam cost for applying to become a
// Verified Trading Hub - same fast-fail-check-plus-authoritative-
// re-deduct pattern as TokenCreationFeeETHTest (see
// api/token_registry.go), paid to registrationFeeAddress. A hub is
// the highest-trust, most user-facing role a wallet can apply for
// short of running a validator, so it costs more than a moderator
// registration - a disclosed judgment call, not a locked spec value.
const HubApplyFeeCCT = 25

type companyApplyData struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	DocumentNote string `json:"documentNote"`
	// ContactEmail is optional - see core.CompanyApplication's doc
	// comment. Left "" if the applicant doesn't want email
	// notifications (in-app notifications still work regardless).
	ContactEmail string `json:"contactEmail"`
}

// HandleApplyCompany lets any wallet apply for the testnet "Verified"
// badge - free-text context only, reviewed by whichever moderator gets
// assigned (same round-robin as every other moderator-gated feature).
func HandleApplyCompany(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req companyApplyData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Name) == 0 || len(req.Name) > 200 {
		http.Error(w, "name must be 1-200 characters", http.StatusBadRequest)
		return
	}
	if len(req.Description) > 2000 || len(req.DocumentNote) > 2000 {
		http.Error(w, "description and documentNote must each be under 2000 characters", http.StatusBadRequest)
		return
	}
	if len(req.ContactEmail) > 200 {
		http.Error(w, "contactEmail must be under 200 characters", http.StatusBadRequest)
		return
	}

	if _, exists := companyStore.Get(address); exists {
		http.Error(w, "an application already exists for this address", http.StatusBadRequest)
		return
	}

	if tokenStore.BalanceOf(address) < HubApplyFeeCCT {
		http.Error(w, fmt.Sprintf("you need at least %d CCT to cover the hub application fee", HubApplyFeeCCT), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyApply, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListCompanies is a public read of every application regardless
// of status, same reasoning as every other list endpoint in this app -
// a rejected application stays visible rather than vanishing.
func HandleListCompanies(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(companyStore.List())
}

type companyReviewData struct {
	Address string `json:"address"`
	Approve bool   `json:"approve"`
}

// HandleReviewCompany is the assigned moderator's approve/reject
// decision - only the assigned moderator may answer, and only once.
func HandleReviewCompany(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req companyReviewData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	app, ok := companyStore.Get(req.Address)
	if !ok {
		http.Error(w, "application not found", http.StatusNotFound)
		return
	}
	if app.ModeratorAddress != address {
		http.Error(w, "this application wasn't assigned to you", http.StatusForbidden)
		return
	}
	if app.Status != core.CompanyPending {
		http.Error(w, "this application has already been reviewed", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyReview, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type companyUpdateContactData struct {
	ContactEmail string `json:"contactEmail"`
}

// HandleUpdateCompanyContact lets an applicant/hub change where they
// get emailed trade-attention notifications - available regardless of
// application status, and not moderator-gated (see
// core.CompanyStore.SetContactEmail's doc comment).
func HandleUpdateCompanyContact(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req companyUpdateContactData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.ContactEmail) > 200 {
		http.Error(w, "contactEmail must be under 200 characters", http.StatusBadRequest)
		return
	}
	if _, ok := companyStore.Get(address); !ok {
		http.Error(w, "no application on file for this address", http.StatusNotFound)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyUpdateContact, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type companyReassignData struct {
	Address string `json:"address"`
}

// HandleReassignCompanyModerator lets any currently-registered
// moderator claim a still-PENDING application away from whoever it was
// originally assigned to - the fix for an application getting
// permanently stuck if its assigned moderator's key is lost or the
// address is otherwise unreachable (see
// core.CompanyStore.ReassignModerator's doc comment). Only works while
// still PENDING - a real decision already on record can't be reopened
// this way.
func HandleReassignCompanyModerator(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req companyReassignData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !moderatorStore.IsModerator(address) {
		http.Error(w, "only a registered moderator can claim an application", http.StatusForbidden)
		return
	}

	app, ok := companyStore.Get(req.Address)
	if !ok {
		http.Error(w, "application not found", http.StatusNotFound)
		return
	}
	if app.Status != core.CompanyPending {
		http.Error(w, "this application has already been reviewed", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyReassignModerator, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

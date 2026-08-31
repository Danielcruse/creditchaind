package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// This file is the testnet-only "Verified Trading Company" placeholder
// - see core/company.go's doc comment for the scope and its explicit
// limits. Nothing here performs real identity verification, and none
// of it should ever be presented to a user as real KYC/AML compliance.

var companyStore = core.NewCompanyStore()

// HubApplyFeeCCT is the ONLY fee for the whole hub-registration flow,
// request through approval (this session's fee review consolidated
// what used to be two separate fees - this one plus a 500 CCT
// approval-time fee - into just this one) - same fast-fail-check-plus-
// authoritative-re-deduct pattern as TokenCreationFeeETHTest (see
// api/token_registry.go). Split 40% Foundation Treasury (admin) / 40%
// the assigned moderator / 20% into the CCT validator pool at
// application time (see api/apply.go's applyCompanyApply) - a hub is
// the highest-trust, most user-facing role a wallet can apply for
// short of running a validator, so it costs more than a moderator
// registration - a disclosed judgment call, not a locked spec value.
const HubApplyFeeCCT = 25

// HubMinimumStakeCCT is the minimum CCT a wallet must already have
// staked (see stakingStore) before it can even apply to become a
// Trading Hub - a real "skin in the game" bar, separate from the flat
// anti-spam HubApplyFeeCCT above. Checked at both application time
// (fast-fail, here) and again at confirmation (applyCompanyApply,
// api/apply.go), never trusted from submission time alone.
const HubMinimumStakeCCT = 3000

// HubStakeVaultMaxCCT is the most a hub is required to lock into its
// dedicated stake-vault (core.StakePurposeHub, see
// core/stake_purpose_store.go) once a moderator approves its
// application - reserved out of the hub's own already-staked CCT
// balance, not a new deposit. A hub staking less than this at
// approval time locks whatever it actually has instead (see
// applyCompanyReview).
const HubStakeVaultMaxCCT = 5000

// maxCertificateFileBytes bounds the real (decoded) certificate file
// size accepted by HandleUploadCertificate - 5MB, a disclosed
// judgment call sized for a scanned document/photo, not a hard spec
// value.
const maxCertificateFileBytes = 5 * 1024 * 1024

// certificateAllowedExtensions is the file-type allowlist for
// uploaded certificates - kept narrow (documents/images only) rather
// than accepting arbitrary file types.
var certificateAllowedExtensions = map[string]bool{
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true,
}

type companyApplyData struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	DocumentNote string `json:"documentNote"`
	// ContactEmail is optional - see core.CompanyApplication's doc
	// comment. Left "" if the applicant doesn't want email
	// notifications (in-app notifications still work regardless).
	ContactEmail string `json:"contactEmail"`
	// Country is the applicant's declared home jurisdiction - a required
	// dropdown selection from core.AllowedHubCountries, which has no
	// United States entry at all (see that list's own doc comment).
	// Rejected here, at submission time, rather than only at approval
	// time, so a US-jurisdiction applicant gets a clear reason
	// immediately and never pays the application fee for something that
	// can never be approved (see core.CompanyStore.Review's identical
	// hard block).
	Country string `json:"country"`
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
	if !core.IsAllowedHubCountry(req.Country) {
		http.Error(w, "country must be a valid selection from the list - the United States is not a supported jurisdiction for a Trading Hub on this platform (no US money-transmitter/MSB license is held anywhere)", http.StatusForbidden)
		return
	}

	if _, exists := companyStore.Get(address); exists {
		http.Error(w, "an application already exists for this address", http.StatusBadRequest)
		return
	}

	if !hasCctBalance(address, HubApplyFeeCCT) {
		http.Error(w, fmt.Sprintf("you need at least %d CCT to cover the hub application fee", HubApplyFeeCCT), http.StatusBadRequest)
		return
	}

	if stakingStore.StakeOf(address, "CCT") < HubMinimumStakeCCT {
		http.Error(w, fmt.Sprintf("you need at least %d CCT staked before applying to become a Trading Hub", HubMinimumStakeCCT), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyApply, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListHubCountries is the single source of truth the Trading Hub
// application form's country dropdown populates from (see
// website/js/companies.js) - never hardcoded client-side, so the
// frontend's list can never drift from what core.IsAllowedHubCountry
// (the actual server-side enforcement, including the "no United States
// option exists at all" rule) will accept.
func HandleListHubCountries(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(core.AllowedHubCountries)
}

// HandleListCompanies is a public read of every application regardless
// of status, same reasoning as every other list endpoint in this app -
// a rejected application stays visible rather than vanishing.
func HandleListCompanies(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(companyStore.List())
}

// HandleCompanyStatus is the lightweight per-address lookup the
// sidebar uses to decide whether to show the real Trading Hub button
// or just an "Apply Here" link (see website/js/outernet.js) - cheaper
// than fetching and filtering the full list client-side for what's
// ultimately a single yes/no gate.
func HandleCompanyStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	app, ok := companyStore.Get(address)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"exists": false})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"exists":   true,
		"status":   app.Status,
		"verified": app.Status == core.CompanyVerified,
		// certificateUploaded lets the applicant's own page tell them
		// they still need to upload one - without this, a still-PENDING
		// application with no certificate on file looked identical to
		// one just waiting on the moderator, with no way for the
		// applicant to know approval can never succeed until they act
		// (see applyCompanyReview's certificate check). Only the
		// presence of a hash is exposed, never the hash itself.
		"certificateUploaded": app.CertificateHash != "",
	})
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
	// Re-checked at apply time too (applyCompanyReview) - that check is
	// what actually matters for consensus-correctness, since
	// submission-time state can go stale before the tx confirms. But
	// letting an approval that will DEFINITELY fail silently record as
	// "pending" anyway means the moderator sees nothing ever happen with
	// no explanation - checking here too means they get a real error
	// immediately instead. No approval-time fee balance check anymore -
	// this session's fee review consolidated hub registration down to
	// the single HubApplyFeeCCT already charged (and split among
	// admin/moderator/validator) at application time.
	if req.Approve && app.CertificateHash == "" {
		http.Error(w, "this hub hasn't uploaded a certificate yet - it can't be approved until it does", http.StatusBadRequest)
		return
	}
	// No moderator may approve a Trading Hub whose declared jurisdiction
	// isn't on core.AllowedHubCountries - which has no United States
	// entry at all (see that list's own doc comment). Checked here too,
	// not just in core.CompanyStore.Review (the real enforcement), so a
	// moderator gets an explicit reason immediately instead of watching
	// their approval silently never take effect.
	if req.Approve && !core.IsAllowedHubCountry(app.Country) {
		http.Error(w, "this application's declared jurisdiction is not eligible for approval - the United States is not a supported jurisdiction for a Trading Hub on this platform (no US money-transmitter/MSB license is held anywhere); approving this application is not possible regardless of moderator judgment", http.StatusForbidden)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyReview, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type companyUploadCertificateData struct {
	// FileName is used only to derive the extension for the allowlist
	// check and the stored file's own extension - never trusted as an
	// actual filesystem path.
	FileName   string `json:"fileName"`
	FileBase64 string `json:"fileBase64"`
}

// HandleUploadCertificate accepts a real certificate document from a
// still-PENDING applicant - base64-encoded inside an ordinary signed
// JSON request (this codebase's existing requireSignedRequest
// Ed25519-envelope model has no established multipart-plus-signature
// convention, so this reuses that exact mechanism rather than
// inventing a new auth scheme just for this one endpoint) rather than
// a raw multipart upload. Decoded, size/type-checked, hashed, and
// written to disk here at the HTTP layer so a fast-fail response is
// possible before ever recording a transaction; applyCompanyUploadCertificate
// (api/apply.go) re-does the same decode/validate/write at
// confirmation time, never trusting this pass alone, matching this
// codebase's blanket "never trust submission-time state" discipline.
func HandleUploadCertificate(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req companyUploadCertificateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	app, ok := companyStore.Get(address)
	if !ok || app.Status != core.CompanyPending {
		http.Error(w, "no pending application on file for this address", http.StatusBadRequest)
		return
	}

	if _, _, err := decodeAndValidateCertificate(req.FileName, req.FileBase64); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Fail closed here too, not just at confirmation time
	// (applyCompanyUploadCertificate) - a submission that's guaranteed
	// to no-op at apply time should never look "pending" to the
	// applicant with no explanation. See hubKYCEncryptionKey's doc
	// comment for what this key protects and why a hub certificate
	// (a real, sensitive uploaded document) is never written to disk
	// unencrypted.
	if _, err := hubKYCEncryptionKey(); err != nil {
		http.Error(w, "certificate encryption is not configured on this node yet - contact the node operator", http.StatusServiceUnavailable)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxCompanyUploadCertificate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// decodeAndValidateCertificate is shared by the HTTP fast-fail check
// and the apply-time confirmation - decodes the base64 payload,
// enforces the size cap and extension allowlist, and returns the raw
// bytes plus the extension (including the leading dot) actually used.
// Never writes anything to disk itself.
func decodeAndValidateCertificate(fileName, fileBase64 string) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	if !certificateAllowedExtensions[ext] {
		return nil, "", fmt.Errorf("unsupported file type %q - only PDF/PNG/JPG certificates are accepted", ext)
	}

	raw, err := base64.StdEncoding.DecodeString(fileBase64)
	if err != nil {
		return nil, "", fmt.Errorf("could not decode file data")
	}
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("file is empty")
	}
	if len(raw) > maxCertificateFileBytes {
		return nil, "", fmt.Errorf("certificate file exceeds the %d MB limit", maxCertificateFileBytes/(1024*1024))
	}

	return raw, ext, nil
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

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetModeratorVerificationTestState resets every store the
// applyCompanyReview path touches, on top of the plain-transfer state
// resetApplyTestState already clears.
func resetModeratorVerificationTestState() {
	resetApplyTestState()
	companyStore = core.NewCompanyStore()
	moderatorStore = core.NewModeratorStore()
	moderatorVerificationLog = core.NewModeratorVerificationLog()
}

func TestApplyCompanyReview_AppendsLedgerEntryOnApproveAndReject(t *testing.T) {
	resetModeratorVerificationTestState()

	const moderator = "mvl-test-moderator"
	const hubApprove = "mvl-test-hub-approve"
	const hubReject = "mvl-test-hub-reject"

	for _, addr := range []string{hubApprove, hubReject} {
		companyStore.Apply(&core.CompanyApplication{
			Address:          addr,
			Name:             "Test Hub " + addr,
			Status:           core.CompanyPending,
			ModeratorAddress: moderator,
		})
		app, _ := companyStore.Get(addr)
		app.CertificateHash = "hash-" + addr
	}
	approveData, _ := json.Marshal(companyReviewData{Address: hubApprove, Approve: true})
	applyCompanyReview(core.Transaction{
		ID:        "test-review-approve",
		From:      moderator,
		Type:      core.TxCompanyReview,
		Data:      string(approveData),
		Timestamp: 1000,
	})

	rejectData, _ := json.Marshal(companyReviewData{Address: hubReject, Approve: false})
	applyCompanyReview(core.Transaction{
		ID:        "test-review-reject",
		From:      moderator,
		Type:      core.TxCompanyReview,
		Data:      string(rejectData),
		Timestamp: 1001,
	})

	entries := moderatorVerificationLog.ForModerator(moderator)
	if len(entries) != 2 {
		t.Fatalf("expected 2 ledger entries, got %d", len(entries))
	}
	// ForModerator returns newest first.
	if entries[0].CompanyAddress != hubReject || entries[0].Approved {
		t.Fatalf("expected newest entry to be the reject decision, got %+v", entries[0])
	}
	if entries[1].CompanyAddress != hubApprove || !entries[1].Approved {
		t.Fatalf("expected oldest entry to be the approve decision, got %+v", entries[1])
	}
	if entries[1].CertificateHash != "hash-"+hubApprove {
		t.Fatalf("expected certificate hash recorded, got %q", entries[1].CertificateHash)
	}
}

func TestApplyCompanyReview_NoLedgerEntryWhenReviewRejectedByStore(t *testing.T) {
	resetModeratorVerificationTestState()

	// No application exists for this address at all - companyStore.Review
	// (and the earlier fast-fail checks) must reject before anything is
	// appended to the ledger.
	data, _ := json.Marshal(companyReviewData{Address: "mvl-nonexistent-hub", Approve: true})
	applyCompanyReview(core.Transaction{
		ID:        "test-review-nonexistent",
		From:      "some-moderator",
		Type:      core.TxCompanyReview,
		Data:      string(data),
		Timestamp: 1000,
	})

	if entries := moderatorVerificationLog.ForModerator("some-moderator"); len(entries) != 0 {
		t.Fatalf("expected no ledger entries for a rejected review, got %d", len(entries))
	}
}

func TestHandleModeratorVerifications_GatedToExecutives(t *testing.T) {
	resetModeratorVerificationTestState()

	execPub, execPriv, _ := core.GenerateKeyPair()
	execAddr := core.GenerateAddress(execPub)
	reptStore.Mint(execAddr, 1000) // puts this wallet in the top-N reputation committee

	nonExecPub, nonExecPriv, _ := core.GenerateKeyPair()

	const moderator = "mvl-http-test-moderator"
	moderatorVerificationLog.Append(&core.ModeratorVerificationEntry{
		ID:               "seed-entry",
		ModeratorAddress: moderator,
		CompanyAddress:   "some-hub",
		CertificateHash:  "some-hash",
		Approved:         true,
		Timestamp:        1000,
	})

	// A non-executive caller must be rejected, even though they are
	// otherwise a genuinely signed, valid caller.
	nonExecBody := buildSignedEnvelopeJSON(t, nonExecPub, nonExecPriv, moderatorVerificationsRequest{Moderator: moderator})
	nonExecReq := httptest.NewRequest(http.MethodPost, "/governance/moderator-verifications", bytes.NewReader(nonExecBody))
	nonExecRec := httptest.NewRecorder()
	HandleModeratorVerifications(nonExecRec, nonExecReq)
	if nonExecRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-executive caller, got %d: %s", nonExecRec.Code, nonExecRec.Body.String())
	}

	// A genuine top-reputation executive must succeed and see the entry.
	execBody := buildSignedEnvelopeJSON(t, execPub, execPriv, moderatorVerificationsRequest{Moderator: moderator})
	execReq := httptest.NewRequest(http.MethodPost, "/governance/moderator-verifications", bytes.NewReader(execBody))
	execRec := httptest.NewRecorder()
	HandleModeratorVerifications(execRec, execReq)
	if execRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an executive caller, got %d: %s", execRec.Code, execRec.Body.String())
	}

	var resp struct {
		Entries []*core.ModeratorVerificationEntry `json:"entries"`
	}
	if err := json.Unmarshal(execRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ID != "seed-entry" {
		t.Fatalf("expected the seeded entry to be returned, got %+v", resp.Entries)
	}
}

package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// buildSignedEnvelopeJSONAt is buildSignedEnvelopeJSON with an explicit
// timestamp instead of time.Now() - needed because rememberSignature
// (api/auth_middleware.go) correctly treats two identical
// address+publicKey+timestamp+payload requests as a replay, and this
// test file deliberately signs the same read-only payload (e.g.
// hubKYCHubOnlyData{Hub: X}) more than once in quick succession to
// check state before/after a mutation - real distinct timestamps keep
// those from colliding.
func buildSignedEnvelopeJSONAt(t *testing.T, pub, priv []byte, payload any, timestamp int64) []byte {
	t.Helper()

	from := core.GenerateAddress(pub)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: timestamp,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())
	env.Signature = hex.EncodeToString(sig)

	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

// setHubKYCTestEncryptionKey gives each test a real 32-byte key so the
// encrypt/decrypt round trip in api/hub_kyc.go actually exercises AES-
// GCM rather than short-circuiting on the "not configured" error path,
// and restores whatever was there before so this doesn't leak into
// other tests in the package.
func setHubKYCTestEncryptionKey(t *testing.T) {
	t.Helper()
	prev, hadPrev := os.LookupEnv("HUB_KYC_ENCRYPTION_KEY_HEX")
	os.Setenv("HUB_KYC_ENCRYPTION_KEY_HEX", strings.Repeat("ab", 32))
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("HUB_KYC_ENCRYPTION_KEY_HEX", prev)
		} else {
			os.Unsetenv("HUB_KYC_ENCRYPTION_KEY_HEX")
		}
	})
}

func TestSubmitHubKYC_UnverifiedHubRejected(t *testing.T) {
	resetApplyTestState()
	setHubKYCTestEncryptionKey(t)

	traderPub, traderPriv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubKYCSubmitData{
		Hub: "not-a-real-hub", Phone: "+2348012345678", IDType: "passport", IDNumber: "A1234567",
	})
	req := httptest.NewRequest(http.MethodPost, "/hub-kyc/submit", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleSubmitHubKYC(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unverified hub, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHubKYC_SubmitThenOnlyHubCanList is the core access-control
// property this whole feature exists for: a trader can submit their
// own phone/ID to a verified hub, the hub itself can read it back, but
// a different, unrelated wallet - even a genuinely signed one - cannot.
func TestHubKYC_SubmitThenOnlyHubCanList(t *testing.T) {
	resetApplyTestState()
	setHubKYCTestEncryptionKey(t)

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, Status: core.CompanyVerified})

	traderPub, traderPriv, _ := core.GenerateKeyPair()
	traderAddr := core.GenerateAddress(traderPub)

	submitBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubKYCSubmitData{
		Hub: hubAddr, Phone: "+2348012345678", IDType: "passport", IDNumber: "A1234567",
	})
	submitReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/submit", bytes.NewReader(submitBody))
	submitRec := httptest.NewRecorder()
	HandleSubmitHubKYC(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("expected 200 submitting KYC to a verified hub, got %d: %s", submitRec.Code, submitRec.Body.String())
	}

	// An unrelated wallet must be refused, even though its signature is
	// perfectly valid - it just isn't the hub these records belong to.
	strangerPub, strangerPriv, _ := core.GenerateKeyPair()
	strangerBody := buildSignedEnvelopeJSON(t, strangerPub, strangerPriv, hubKYCHubOnlyData{Hub: hubAddr})
	strangerReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/list", bytes.NewReader(strangerBody))
	strangerRec := httptest.NewRecorder()
	HandleListHubKYC(strangerRec, strangerReq)
	if strangerRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-hub caller, got %d: %s", strangerRec.Code, strangerRec.Body.String())
	}

	// The hub itself must see exactly the record the trader submitted.
	hubBody := buildSignedEnvelopeJSON(t, hubPub, hubPriv, hubKYCHubOnlyData{Hub: hubAddr})
	hubReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/list", bytes.NewReader(hubBody))
	hubRec := httptest.NewRecorder()
	HandleListHubKYC(hubRec, hubReq)
	if hubRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the owning hub, got %d: %s", hubRec.Code, hubRec.Body.String())
	}

	var records []hubKYCRecord
	if err := json.Unmarshal(hubRec.Body.Bytes(), &records); err != nil {
		t.Fatalf("decode records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(records))
	}
	if records[0].TraderAddress != traderAddr || records[0].Phone != "+2348012345678" || records[0].IDNumber != "A1234567" {
		t.Fatalf("unexpected record contents: %+v", records[0])
	}

	// A resubmission from the same trader must replace, not duplicate.
	resubmitBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubKYCSubmitData{
		Hub: hubAddr, Phone: "+2348099999999", IDType: "national_id", IDNumber: "B7654321",
	})
	resubmitReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/submit", bytes.NewReader(resubmitBody))
	resubmitRec := httptest.NewRecorder()
	HandleSubmitHubKYC(resubmitRec, resubmitReq)
	if resubmitRec.Code != http.StatusOK {
		t.Fatalf("expected 200 resubmitting KYC, got %d: %s", resubmitRec.Code, resubmitRec.Body.String())
	}

	hubBody2 := buildSignedEnvelopeJSONAt(t, hubPub, hubPriv, hubKYCHubOnlyData{Hub: hubAddr}, time.Now().Unix()+1)
	hubReq2 := httptest.NewRequest(http.MethodPost, "/hub-kyc/list", bytes.NewReader(hubBody2))
	hubRec2 := httptest.NewRecorder()
	HandleListHubKYC(hubRec2, hubReq2)
	var records2 []hubKYCRecord
	if err := json.Unmarshal(hubRec2.Body.Bytes(), &records2); err != nil {
		t.Fatalf("decode records: %v", err)
	}
	if len(records2) != 1 {
		t.Fatalf("expected resubmission to replace, not add - got %d records", len(records2))
	}
	if records2[0].Phone != "+2348099999999" || records2[0].IDNumber != "B7654321" {
		t.Fatalf("expected replaced contents, got %+v", records2[0])
	}
}

func TestHubKYCStatus_ReflectsWhetherCallerSubmitted(t *testing.T) {
	resetApplyTestState()
	setHubKYCTestEncryptionKey(t)

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	_ = hubPriv
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, Status: core.CompanyVerified})

	traderPub, traderPriv, _ := core.GenerateKeyPair()

	statusBeforeBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubKYCHubOnlyData{Hub: hubAddr})
	statusBeforeReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/status", bytes.NewReader(statusBeforeBody))
	statusBeforeRec := httptest.NewRecorder()
	HandleHubKYCStatus(statusBeforeRec, statusBeforeReq)
	var before map[string]bool
	json.Unmarshal(statusBeforeRec.Body.Bytes(), &before)
	if before["submitted"] {
		t.Fatalf("expected submitted=false before any submission")
	}

	submitBody := buildSignedEnvelopeJSON(t, traderPub, traderPriv, hubKYCSubmitData{
		Hub: hubAddr, Phone: "+2348012345678", IDType: "passport", IDNumber: "A1234567",
	})
	submitReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/submit", bytes.NewReader(submitBody))
	submitRec := httptest.NewRecorder()
	HandleSubmitHubKYC(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", submitRec.Code, submitRec.Body.String())
	}

	statusAfterBody := buildSignedEnvelopeJSONAt(t, traderPub, traderPriv, hubKYCHubOnlyData{Hub: hubAddr}, time.Now().Unix()+1)
	statusAfterReq := httptest.NewRequest(http.MethodPost, "/hub-kyc/status", bytes.NewReader(statusAfterBody))
	statusAfterRec := httptest.NewRecorder()
	HandleHubKYCStatus(statusAfterRec, statusAfterReq)
	var after map[string]bool
	json.Unmarshal(statusAfterRec.Body.Bytes(), &after)
	if !after["submitted"] {
		t.Fatalf("expected submitted=true after submission")
	}
}

func TestHubKYCEncryption_RoundTripsAndFileIsNotPlaintext(t *testing.T) {
	setHubKYCTestEncryptionKey(t)

	dir := t.TempDir()
	os.Setenv("DATA_DIR", dir)
	t.Cleanup(func() { os.Unsetenv("DATA_DIR") })

	records := []hubKYCRecord{{TraderAddress: "trader1", Phone: "+15551234567", IDType: "drivers_license", IDNumber: "DL-SECRET-999"}}
	if err := saveHubKYCRecords("hub1", records); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(hubKYCPath("hub1"))
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if bytes.Contains(raw, []byte("DL-SECRET-999")) || bytes.Contains(raw, []byte("+15551234567")) {
		t.Fatalf("hub KYC file on disk contains plaintext PII - encryption is not actually applied")
	}

	loaded, err := loadHubKYCRecords("hub1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].IDNumber != "DL-SECRET-999" {
		t.Fatalf("round trip did not recover original data: %+v", loaded)
	}
}

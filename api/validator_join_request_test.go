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

// envelopeForKey builds a valid SignedEnvelope for an ALREADY-KNOWN
// keypair (unlike adminSignedEnvelope, which always mints a fresh
// random one) - needed here because the admin wallet has to sign two
// different payloads (list, then mark-handled) as the SAME address.
func envelopeForKey(t *testing.T, pub, priv []byte, address string, payload any) core.SignedEnvelope {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	e := core.SignedEnvelope{
		Address:   address,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, e.CanonicalMessage())
	e.Signature = hex.EncodeToString(sig)
	return e
}

// resetValidatorJoinRequestsForTest clears the package-level map so
// tests don't see requests left behind by other tests/a prior run's
// persisted validator_join_requests.json - mirrors resetApplyTestState's
// own reasoning for every other package-level store.
func resetValidatorJoinRequestsForTest() {
	validatorJoinRequestsMu.Lock()
	validatorJoinRequests = map[string]*validatorJoinRequest{}
	validatorJoinRequestsMu.Unlock()
}

func TestHandleSubmitValidatorJoinRequest_RequiresNodeURL(t *testing.T) {
	resetValidatorJoinRequestsForTest()

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, validatorJoinRequestSubmitData{NodeURL: "", Note: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/validator/join-request/submit", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	HandleSubmitValidatorJoinRequest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing nodeUrl, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSubmitValidatorJoinRequest_SucceedsAndIsListableByAdmin(t *testing.T) {
	resetValidatorJoinRequestsForTest()

	applicantPub, applicantPriv, _ := core.GenerateKeyPair()
	applicantAddr := core.GenerateAddress(applicantPub)

	submitBody := buildSignedEnvelopeJSON(t, applicantPub, applicantPriv, validatorJoinRequestSubmitData{
		NodeURL: "https://my-node.example.com:8080",
		Note:    "running on a Lightsail box",
	})
	submitReq := httptest.NewRequest(http.MethodPost, "/validator/join-request/submit", bytes.NewReader(submitBody))
	submitRec := httptest.NewRecorder()
	HandleSubmitValidatorJoinRequest(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a valid join request, got %d: %s", submitRec.Code, submitRec.Body.String())
	}
	var submitResp map[string]string
	if err := json.Unmarshal(submitRec.Body.Bytes(), &submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitResp["id"] == "" {
		t.Fatalf("expected a non-empty request id, got %+v", submitResp)
	}

	// A non-admin wallet must not be able to list requests - sign with
	// a real keypair, but configure a DIFFERENT address as the admin
	// wallet so it's genuinely "signed by someone who isn't the admin".
	nonAdminEnv, nonAdminAddr := adminSignedEnvelope(t, map[string]any{})
	t.Setenv("ADMIN_WALLET_ADDRESS", nonAdminAddr+"-not-the-signer")
	nonAdminBody, _ := json.Marshal(nonAdminEnv)
	nonAdminReq := httptest.NewRequest(http.MethodPost, "/validator/join-requests", bytes.NewReader(nonAdminBody))
	nonAdminRec := httptest.NewRecorder()
	HandleListValidatorJoinRequests(nonAdminRec, nonAdminReq)
	if nonAdminRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin listing join requests, got %d: %s", nonAdminRec.Code, nonAdminRec.Body.String())
	}

	// The configured admin wallet can list it.
	adminPub, adminPriv, _ := core.GenerateKeyPair()
	adminAddr := core.GenerateAddress(adminPub)
	t.Setenv("ADMIN_WALLET_ADDRESS", adminAddr)
	adminEnv := envelopeForKey(t, adminPub, adminPriv, adminAddr, map[string]any{})
	listBody, _ := json.Marshal(adminEnv)
	listReq := httptest.NewRequest(http.MethodPost, "/validator/join-requests", bytes.NewReader(listBody))
	listRec := httptest.NewRecorder()
	HandleListValidatorJoinRequests(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the admin listing join requests, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var list []*validatorJoinRequest
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list) != 1 || list[0].Address != applicantAddr || list[0].NodeURL != "https://my-node.example.com:8080" || list[0].Status != "PENDING" {
		t.Fatalf("expected exactly the one PENDING request just submitted, got %+v", list)
	}

	// Marking it handled updates status and is reflected on the next list -
	// signed by the SAME admin keypair used to list above.
	handledEnv := envelopeForKey(t, adminPub, adminPriv, adminAddr, validatorJoinRequestMarkHandledData{ID: list[0].ID})
	handledBody, _ := json.Marshal(handledEnv)
	handledReq := httptest.NewRequest(http.MethodPost, "/validator/join-request/mark-handled", bytes.NewReader(handledBody))
	handledRec := httptest.NewRecorder()
	HandleMarkValidatorJoinRequestHandled(handledRec, handledReq)
	if handledRec.Code != http.StatusOK {
		t.Fatalf("expected 200 marking the request handled, got %d: %s", handledRec.Code, handledRec.Body.String())
	}

	validatorJoinRequestsMu.Lock()
	got := validatorJoinRequests[list[0].ID].Status
	validatorJoinRequestsMu.Unlock()
	if got != "HANDLED" {
		t.Fatalf("expected status HANDLED after marking, got %q", got)
	}
}

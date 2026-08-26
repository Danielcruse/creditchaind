package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// withTempAWSCredentialsFile points awsCredentialsFilePath at a fresh
// temp file for the duration of the test, restoring the real path
// afterward - this test must never touch the real server-local
// credentials file this package defaults to.
func withTempAWSCredentialsFile(t *testing.T) string {
	t.Helper()
	before := awsCredentialsFilePath
	path := t.TempDir() + "/aws-credentials-test.env"
	awsCredentialsFilePath = path
	t.Cleanup(func() { awsCredentialsFilePath = before })
	return path
}

// TestHandleAdminSetAWSCredentials_WritesBothKMSFlags is the regression
// test for the real gap this closes: submitting real AWS credentials
// previously only enabled the ETH custodial KMS signer
// (ETH_CUSTODIAL_KMS_ENABLED=true), silently leaving the BTC vault
// signer on its insecure in-memory fallback even though the exact same
// credential is enough to activate both (same secp256k1 KMS key type,
// same CreateKey/GetPublicKey/Sign calls - see api/eth_signer.go and
// api/vault_btc_signer.go).
func TestHandleAdminSetAWSCredentials_WritesBothKMSFlags(t *testing.T) {
	resetApplyTestState()
	path := withTempAWSCredentialsFile(t)

	env, address := adminSignedEnvelope(t, adminSetAWSCredentialsData{
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "supersecret", Region: "us-east-1",
	})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/aws-credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleAdminSetAWSCredentials(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the credentials file to be written: %v", err)
	}
	content := string(written)
	if !bytes.Contains(written, []byte("ETH_CUSTODIAL_KMS_ENABLED=true")) {
		t.Fatalf("expected ETH_CUSTODIAL_KMS_ENABLED=true in the written file, got:\n%s", content)
	}
	if !bytes.Contains(written, []byte("BTC_VAULT_KMS_ENABLED=true")) {
		t.Fatalf("expected BTC_VAULT_KMS_ENABLED=true in the written file, got:\n%s", content)
	}
	if !bytes.Contains(written, []byte("AWS_ACCESS_KEY_ID=AKIAEXAMPLE")) {
		t.Fatalf("expected the access key ID written through, got:\n%s", content)
	}
}

func TestHandleAdminSetAWSCredentials_RejectsNonAdminCaller(t *testing.T) {
	resetApplyTestState()
	path := withTempAWSCredentialsFile(t)
	os.Unsetenv("ADMIN_WALLET_ADDRESS")
	os.Unsetenv("ADMIN_TOKEN")

	env, _ := adminSignedEnvelope(t, adminSetAWSCredentialsData{
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "supersecret", Region: "us-east-1",
	})
	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/aws-credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleAdminSetAWSCredentials(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin caller, got %d", w.Code)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected no credentials file to be written for a rejected request")
	}
}

func TestHandleAdminSetAWSCredentials_RejectsMissingFields(t *testing.T) {
	resetApplyTestState()
	path := withTempAWSCredentialsFile(t)

	env, address := adminSignedEnvelope(t, adminSetAWSCredentialsData{
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "", Region: "us-east-1",
	})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/aws-credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleAdminSetAWSCredentials(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing secretAccessKey, got %d", w.Code)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected no credentials file to be written when a required field is missing")
	}
}

// TestHandleAdminSetAWSCredentials_ResponseNeverEchoesSecret confirms
// the write-only guarantee this handler's own doc comment promises -
// the secret must never appear in the HTTP response body.
func TestHandleAdminSetAWSCredentials_ResponseNeverEchoesSecret(t *testing.T) {
	resetApplyTestState()
	withTempAWSCredentialsFile(t)

	env, address := adminSignedEnvelope(t, adminSetAWSCredentialsData{
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "supersecret-value", Region: "us-east-1",
	})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/aws-credentials", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleAdminSetAWSCredentials(w, req)

	if bytes.Contains(w.Body.Bytes(), []byte("supersecret-value")) {
		t.Fatalf("expected the secret access key to never appear in the response, got: %s", w.Body.String())
	}
}

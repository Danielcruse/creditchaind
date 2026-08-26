package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyCompanyUploadCertificate_WritesEncryptedNotPlaintext is the
// core regression test for closing a real pre-existing gap: hub
// certificates used to be written to disk completely unencrypted.
// Confirms the stored file's bytes are neither the raw document nor
// even readable as the same file type, and that encryptHubKYC's own
// round trip recovers the exact original bytes.
func TestApplyCompanyUploadCertificate_WritesEncryptedNotPlaintext(t *testing.T) {
	resetApplyTestState()
	setHubKYCTestEncryptionKey(t)

	pub, _, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	companyStore.Apply(&core.CompanyApplication{Address: addr, Status: core.CompanyPending})

	raw := []byte("%PDF-1.4 this is definitely not encrypted yet")
	data, _ := json.Marshal(companyUploadCertificateData{
		FileName: "cert.pdf", FileBase64: base64.StdEncoding.EncodeToString(raw),
	})
	// applyCompanyUploadCertificate doesn't re-verify auth itself (that's
	// verifyTxAuth's job, already run before this point in the real
	// pipeline) - only tx.From and tx.Data matter here.
	applyCompanyUploadCertificate(core.Transaction{From: addr, Data: string(data), Timestamp: 1000})

	app, ok := companyStore.Get(addr)
	if !ok || app.CertificateFilePath == "" {
		t.Fatalf("expected a certificate to be recorded")
	}

	onDisk, err := os.ReadFile(app.CertificateFilePath)
	if err != nil {
		t.Fatalf("could not read stored certificate: %v", err)
	}
	if bytes.Contains(onDisk, raw) {
		t.Fatalf("stored certificate file contains the raw plaintext document - not encrypted")
	}

	decrypted, err := decryptHubKYC(onDisk)
	if err != nil {
		t.Fatalf("could not decrypt stored certificate: %v", err)
	}
	if !bytes.Equal(decrypted, raw) {
		t.Fatalf("decrypted certificate does not match the original document")
	}
}

// TestApplyCompanyUploadCertificate_NoOpWithoutEncryptionKey confirms
// this fails closed - no encryption key configured means no file gets
// written at all, never a plaintext fallback.
func TestApplyCompanyUploadCertificate_NoOpWithoutEncryptionKey(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("HUB_KYC_ENCRYPTION_KEY_HEX")

	pub, _, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	companyStore.Apply(&core.CompanyApplication{Address: addr, Status: core.CompanyPending})

	raw := []byte("some certificate bytes")
	data, _ := json.Marshal(companyUploadCertificateData{
		FileName: "cert.pdf", FileBase64: base64.StdEncoding.EncodeToString(raw),
	})
	applyCompanyUploadCertificate(core.Transaction{From: addr, Data: string(data), Timestamp: 1000})

	app, _ := companyStore.Get(addr)
	if app.CertificateFilePath != "" {
		t.Fatalf("expected no certificate to be recorded without an encryption key, got %q", app.CertificateFilePath)
	}
}

// TestHandleUploadCertificate_FastFailsWithoutEncryptionKey confirms
// the HTTP layer rejects the request immediately, rather than
// accepting it and letting it silently no-op at confirmation time.
func TestHandleUploadCertificate_FastFailsWithoutEncryptionKey(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("HUB_KYC_ENCRYPTION_KEY_HEX")

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	companyStore.Apply(&core.CompanyApplication{Address: addr, Status: core.CompanyPending})

	body := buildSignedEnvelopeJSON(t, pub, priv, companyUploadCertificateData{
		FileName: "cert.pdf", FileBase64: base64.StdEncoding.EncodeToString([]byte("some bytes")),
	})
	req := httptest.NewRequest(http.MethodPost, "/companies/upload-certificate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleUploadCertificate(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no encryption key is configured, got %d: %s", rec.Code, rec.Body.String())
	}
}

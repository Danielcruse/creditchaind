package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// buildSignedTxAsKeypair mirrors buildSignedTx, signing with a caller-
// supplied keypair instead of generating a fresh one - needed for a
// request-then-confirm flow, where both transactions must be signed by
// the SAME wallet (address is derived from the public key, so it can't
// be targeted with a different keypair after the fact).
func buildSignedTxAsKeypair(t *testing.T, pub, priv []byte, txType core.TransactionType, payload any, ts int64) (core.Transaction, string, []byte) {
	t.Helper()

	from := core.GenerateAddress(pub)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	tx := core.Transaction{
		ID:            "test-tx-" + string(txType) + "-" + from,
		From:          from,
		Type:          txType,
		Timestamp:     ts,
		Data:          string(payloadJSON),
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
	return tx, from, pub
}

func TestVerifyTxAuth_RequestContactEmailCode_AcceptsMatchingPayload(t *testing.T) {
	resetApplyTestState()
	tx, _, _ := buildSignedTx(t, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: "a@example.com"}, 1000)

	if !verifyTxAuth(tx) {
		t.Fatalf("expected a genuinely signed, matching REQUEST_CONTACT_EMAIL_CODE tx to pass auth")
	}
}

func TestVerifyTxAuth_RequestContactEmailCode_RejectsTamperedEmail(t *testing.T) {
	resetApplyTestState()
	tx, _, _ := buildSignedTx(t, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: "a@example.com"}, 1000)

	// A block producer rewrites Data after the signature was computed
	// over the ORIGINAL payload - AuthPayload still carries the real
	// one, so the mismatch must be caught.
	tx.Data = `{"email":"attacker@example.com"}`

	if verifyTxAuth(tx) {
		t.Fatalf("expected a tampered email to be rejected")
	}
}

func TestVerifyTxAuth_ConfirmContactEmail_AcceptsMatchingPayload(t *testing.T) {
	resetApplyTestState()
	tx, _, _ := buildSignedTx(t, core.TxConfirmContactEmail, confirmContactEmailData{Code: "123456"}, 1000)

	if !verifyTxAuth(tx) {
		t.Fatalf("expected a genuinely signed, matching CONFIRM_CONTACT_EMAIL tx to pass auth")
	}
}

func TestVerifyTxAuth_ConfirmContactEmail_RejectsTamperedCode(t *testing.T) {
	resetApplyTestState()
	tx, _, _ := buildSignedTx(t, core.TxConfirmContactEmail, confirmContactEmailData{Code: "123456"}, 1000)
	tx.Data = `{"code":"999999"}`

	if verifyTxAuth(tx) {
		t.Fatalf("expected a tampered code to be rejected")
	}
}

func TestApplyRequestContactEmailCode_SetsPendingStateEvenWithoutSMTPConfigured(t *testing.T) {
	resetApplyTestState()
	// SMTP deliberately left unconfigured - proves the state write
	// doesn't depend on email delivery succeeding (or even being
	// attempted), same "always record real state, sending is best-
	// effort on top" pattern as every other notification in this
	// codebase.
	tx, from, _ := buildSignedTx(t, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: "a@example.com"}, 1000)

	applyRequestContactEmailCode(tx)

	rec, ok := recoveryStore.Get(from)
	if !ok {
		t.Fatalf("expected a recovery record to be created")
	}
	if rec.PendingEmail != "a@example.com" {
		t.Fatalf("expected PendingEmail set, got %+v", rec)
	}
	if rec.PendingEmailToken == "" || len(rec.PendingEmailToken) != 6 {
		t.Fatalf("expected a 6-digit code generated, got %q", rec.PendingEmailToken)
	}
	if rec.PendingEmailTokenExpiresAt != 1000+contactEmailCodeValiditySeconds {
		t.Fatalf("expected expiry = tx.Timestamp + validity window, got %d", rec.PendingEmailTokenExpiresAt)
	}
	if rec.EmailVerified {
		t.Fatalf("expected EmailVerified to stay false until confirmed")
	}
}

func TestApplyRequestContactEmailCode_RejectsEmptyOrOverlongEmail(t *testing.T) {
	resetApplyTestState()

	tx, from, _ := buildSignedTx(t, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: ""}, 1000)
	applyRequestContactEmailCode(tx)
	if _, ok := recoveryStore.Get(from); ok {
		t.Fatalf("expected no record created for an empty email")
	}

	tooLong := strings.Repeat("a", 201) + "@example.com"
	tx2, from2, _ := buildSignedTx(t, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: tooLong}, 1000)
	applyRequestContactEmailCode(tx2)
	if _, ok := recoveryStore.Get(from2); ok {
		t.Fatalf("expected no record created for an over-200-char email")
	}
}

// TestContactEmailFlow_EndToEnd_ClosesTheRealReportedGap is the
// regression test for the actual bug: after retiring wallet recovery,
// NOTHING could ever set EmailVerified=true again, silently starving
// api/validator_application.go, api/p2p_trade_start_notify.go, and
// api/p2p_dispute_notify.go - all three gate their notification on
// exactly this flag. This proves the new request->confirm flow reaches
// the same end state those three consumers already correctly check for.
func TestContactEmailFlow_EndToEnd_ClosesTheRealReportedGap(t *testing.T) {
	resetApplyTestState()

	pub, priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}

	reqTx, from, _ := buildSignedTxAsKeypair(t, pub, priv, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: "user@example.com"}, 1000)
	applyRequestContactEmailCode(reqTx)

	rec, ok := recoveryStore.Get(from)
	if !ok || rec.PendingEmailToken == "" {
		t.Fatalf("setup issue: expected a pending code after the request step")
	}
	code := rec.PendingEmailToken

	confirmTx, _, _ := buildSignedTxAsKeypair(t, pub, priv, core.TxConfirmContactEmail, confirmContactEmailData{Code: code}, 1100)
	applyConfirmContactEmail(confirmTx)

	rec, _ = recoveryStore.Get(from)
	if !rec.EmailVerified || rec.Email != "user@example.com" {
		t.Fatalf("expected EmailVerified=true and Email set after confirming - this is exactly what the 3 notification consumers check, got %+v", rec)
	}
}

func TestApplyConfirmContactEmail_NoOpOnWrongCode(t *testing.T) {
	resetApplyTestState()

	pub, priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}

	reqTx, from, _ := buildSignedTxAsKeypair(t, pub, priv, core.TxRequestContactEmailCode, requestContactEmailCodeData{Email: "user@example.com"}, 1000)
	applyRequestContactEmailCode(reqTx)

	confirmTx, _, _ := buildSignedTxAsKeypair(t, pub, priv, core.TxConfirmContactEmail, confirmContactEmailData{Code: "000000"}, 1100)
	applyConfirmContactEmail(confirmTx)

	rec, _ := recoveryStore.Get(from)
	if rec.EmailVerified {
		t.Fatalf("expected EmailVerified to stay false after a wrong-code confirm attempt")
	}
}

// --- HTTP handler tests ---

func TestHandleRequestContactEmailCode_RejectsWhenSMTPNotConfigured(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSONAt(t, pub, priv, requestContactEmailCodeData{Email: "a@example.com"}, time.Now().Unix())

	req := httptest.NewRequest("POST", "/contact-email/request-code", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleRequestContactEmailCode(rec, req)

	if rec.Code != 503 {
		t.Fatalf("expected 503 when SMTP isn't configured, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRequestContactEmailCode_RejectsInvalidEmail(t *testing.T) {
	resetApplyTestState()
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_USERNAME", "user")
	t.Setenv("SMTP_PASSWORD", "pass")
	t.Setenv("SMTP_FROM", "noreply@example.com")

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSONAt(t, pub, priv, requestContactEmailCodeData{Email: "not-an-email"}, time.Now().Unix())

	req := httptest.NewRequest("POST", "/contact-email/request-code", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleRequestContactEmailCode(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 for an email missing '@', got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRequestContactEmailCode_AcceptsValidRequestWhenSMTPConfigured(t *testing.T) {
	resetApplyTestState()
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_USERNAME", "user")
	t.Setenv("SMTP_PASSWORD", "pass")
	t.Setenv("SMTP_FROM", "noreply@example.com")

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSONAt(t, pub, priv, requestContactEmailCodeData{Email: "a@example.com"}, time.Now().Unix())

	req := httptest.NewRequest("POST", "/contact-email/request-code", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleRequestContactEmailCode(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 for a valid request with SMTP configured, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(mempool.Transactions) != 1 {
		t.Fatalf("expected exactly one pending transaction queued, got %d", len(mempool.Transactions))
	}
}

func TestHandleConfirmContactEmail_RejectsMissingCode(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSONAt(t, pub, priv, confirmContactEmailData{Code: ""}, time.Now().Unix())

	req := httptest.NewRequest("POST", "/contact-email/confirm", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleConfirmContactEmail(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 for a missing code, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetContactEmailStatus_ReflectsPendingThenVerified(t *testing.T) {
	resetApplyTestState()

	const addr = "status-test-addr"
	req := httptest.NewRequest("GET", "/contact-email/status?address="+addr, nil)
	rec := httptest.NewRecorder()
	HandleGetContactEmailStatus(rec, req)
	if !strings.Contains(rec.Body.String(), `"verified":false`) {
		t.Fatalf("expected verified:false for a never-requested address, got %s", rec.Body.String())
	}

	recoveryStore.SetPendingEmail(addr, "a@example.com", "123456", 1000)
	rec2 := httptest.NewRecorder()
	HandleGetContactEmailStatus(rec2, httptest.NewRequest("GET", "/contact-email/status?address="+addr, nil))
	if !strings.Contains(rec2.Body.String(), `"pending":true`) || !strings.Contains(rec2.Body.String(), "a@example.com") {
		t.Fatalf("expected pending:true with the pending email surfaced, got %s", rec2.Body.String())
	}

	recoveryStore.ConfirmPendingEmail(addr, "123456", 999)
	rec3 := httptest.NewRecorder()
	HandleGetContactEmailStatus(rec3, httptest.NewRequest("GET", "/contact-email/status?address="+addr, nil))
	if !strings.Contains(rec3.Body.String(), `"verified":true`) {
		t.Fatalf("expected verified:true after confirmation, got %s", rec3.Body.String())
	}
}

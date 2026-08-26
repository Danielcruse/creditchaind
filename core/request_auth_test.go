package core

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func signedEnvelope(t *testing.T, payload any, ts int64) SignedEnvelope {
	t.Helper()

	pub, priv, _ := GenerateKeyPair()
	address := GenerateAddress(pub)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	e := SignedEnvelope{
		Address:   address,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := SignMessage(priv, e.CanonicalMessage())
	e.Signature = hex.EncodeToString(sig)
	return e
}

func TestSignedEnvelopeVerify_ValidPasses(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Unix())
	if err := e.Verify(); err != nil {
		t.Fatalf("expected valid envelope to verify, got: %v", err)
	}
}

func TestSignedEnvelopeVerify_TamperedPayloadFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Unix())
	// Same signature, different payload bytes - must fail since the
	// signature covers the payload's literal bytes, not just its
	// semantic value.
	e.Payload = json.RawMessage(`{"amount":999}`)
	if err := e.Verify(); err == nil {
		t.Fatal("expected tampered payload to fail verification")
	}
}

func TestSignedEnvelopeVerify_WrongSignerFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Unix())

	otherPub, otherPriv, _ := GenerateKeyPair()
	_ = otherPub
	// Re-sign with a different keypair's private key, but keep the
	// original address/publicKey - simulates someone without the real
	// private key trying to forge a signature.
	badSig := SignMessage(otherPriv, e.CanonicalMessage())
	e.Signature = hex.EncodeToString(badSig)

	if err := e.Verify(); err == nil {
		t.Fatal("expected signature from the wrong key to fail verification")
	}
}

func TestSignedEnvelopeVerify_AddressPublicKeyMismatchFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Unix())

	otherPub, _, _ := GenerateKeyPair()
	// Claim a different public key than the one actually used to sign -
	// address won't match SHA256(publicKey)[:20] anymore.
	e.PublicKey = hex.EncodeToString(otherPub)

	if err := e.Verify(); err == nil {
		t.Fatal("expected address/publicKey mismatch to fail verification")
	}
}

func TestSignedEnvelopeVerify_StaleTimestampFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Add(-2*RequestTimestampWindow).Unix())
	if err := e.Verify(); err == nil {
		t.Fatal("expected a timestamp far in the past to fail verification")
	}
}

func TestSignedEnvelopeVerify_FutureTimestampFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Add(2*RequestTimestampWindow).Unix())
	if err := e.Verify(); err == nil {
		t.Fatal("expected a timestamp far in the future to fail verification")
	}
}

func TestSignedEnvelopeVerifySignatureOnly_IgnoresStaleTimestamp(t *testing.T) {
	// VerifySignatureOnly is what re-verifying an old, already-confirmed
	// on-chain transaction uses (see its doc comment) - a stale
	// timestamp must NOT fail this path, only Verify's freshness check.
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Add(-24*time.Hour).Unix())
	if err := e.VerifySignatureOnly(); err != nil {
		t.Fatalf("expected VerifySignatureOnly to ignore timestamp age, got: %v", err)
	}
}

func TestSignedEnvelopeVerify_InvalidPublicKeyEncodingFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Unix())
	e.PublicKey = "not-hex"
	if err := e.Verify(); err == nil {
		t.Fatal("expected invalid publicKey encoding to fail verification")
	}
}

func TestSignedEnvelopeVerify_InvalidSignatureEncodingFails(t *testing.T) {
	e := signedEnvelope(t, map[string]int{"amount": 5}, time.Now().Unix())
	e.Signature = "not-hex"
	if err := e.Verify(); err == nil {
		t.Fatal("expected invalid signature encoding to fail verification")
	}
}

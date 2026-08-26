package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// RequestTimestampWindow bounds how old/new a signed request's
// timestamp may be. Narrow enough to make a captured signature
// useless for long, wide enough to tolerate normal clock drift
// between client and server.
const RequestTimestampWindow = 60 * time.Second

// SignedEnvelope wraps every mutating API request. Since an address
// is derived as SHA256(publicKey)[:20] (see GenerateAddress), the
// envelope is self-certifying - the server never needs to have seen
// this address before to verify the request actually came from
// whoever holds the private key for it.
type SignedEnvelope struct {
	Address   string          `json:"address"`
	PublicKey string          `json:"publicKey"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// CanonicalMessage is exactly what the client must sign - and exactly
// what the server re-derives and checks the signature against. Any
// mismatch in payload byte-for-byte (not just semantically) fails
// verification, which is intentional: it forces the client to sign
// the literal bytes being sent.
func (e SignedEnvelope) CanonicalMessage() []byte {
	msg := e.Address + "|" + e.PublicKey + "|" + strconv.FormatInt(e.Timestamp, 10) + "|" + string(e.Payload)
	return []byte(msg)
}

// Verify checks that: the claimed address really is derived from the
// supplied public key, the timestamp is within the acceptable window
// of "now", and the signature actually validates against the
// canonical message. It does NOT check for replay - that's a
// server-side concern (a set of recently-seen signatures), since it
// needs shared state across requests that this pure function doesn't
// have.
//
// Use this for a LIVE HTTP request, where "was this signed recently"
// is exactly the property being checked. For re-verifying an envelope
// embedded in an already-confirmed on-chain transaction - which may
// be arbitrarily old by the time another node applies it during a
// catch-up sync - use VerifySignatureOnly instead; the freshness
// window doesn't apply there; a given transaction's own ID/hash-based
// dedup already prevents it being applied more than once.
func (e SignedEnvelope) Verify() error {
	age := time.Since(time.Unix(e.Timestamp, 0))
	if age < -RequestTimestampWindow || age > RequestTimestampWindow {
		return errors.New("timestamp outside acceptable window")
	}

	return e.VerifySignatureOnly()
}

// VerifySignatureOnly checks the same address/publicKey/signature
// binding as Verify, but without the "was this signed recently" bound
// - see Verify's doc comment for when to use which.
func (e SignedEnvelope) VerifySignatureOnly() error {
	pubBytes, err := hex.DecodeString(e.PublicKey)
	if err != nil {
		return errors.New("invalid publicKey encoding")
	}

	if GenerateAddress(pubBytes) != e.Address {
		return errors.New("address does not match publicKey")
	}

	sigBytes, err := hex.DecodeString(e.Signature)
	if err != nil {
		return errors.New("invalid signature encoding")
	}

	if !VerifySignature(pubBytes, e.CanonicalMessage(), sigBytes) {
		return errors.New("signature verification failed")
	}

	return nil
}

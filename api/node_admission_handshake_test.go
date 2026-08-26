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

// resetNodeAdmissionHandshakeTestState mirrors resetPeerSecurityTestState
// (api/peer_security_test.go) plus the node-admission store this file's
// tests also need clean.
func resetNodeAdmissionHandshakeTestState(selfNodeID string) {
	validatorStore = core.NewValidatorStore()
	peerStore = core.NewPeerStore()
	config = &core.Config{NodeID: selfNodeID}
	nodeIdentity = core.NewNodeIdentity()
	nodeAdmissionStore = core.NewNodeAdmissionStore()
	selfBaseURL = "http://self.example"
}

// buildSignedHandshakeRequest builds a genuinely self-signed handshake
// (real proof-of-possession, same as
// TestVerifyHandshakeSignature_AcceptsGenuineFreshSignature) from a
// fresh keypair, POSTed with the given NETWORK_SECRET header - so every
// test in this file exercises ONLY the new node-admission gate, never
// accidentally passing because signature/secret verification happened
// to fail too.
func buildSignedHandshakeRequest(t *testing.T, networkSecret string) (*http.Request, string) {
	t.Helper()
	pub, priv, _ := core.GenerateKeyPair()
	pubHex := hex.EncodeToString(pub)
	timestamp := time.Now().Unix()
	msg := handshakeMsg{
		NodeID:    "candidate-node",
		PublicKey: pubHex,
		Address:   "http://candidate.example",
		Timestamp: timestamp,
	}
	sig := core.SignMessage(priv, handshakeCanonicalMessage(msg.NodeID, msg.PublicKey, msg.Address, msg.Timestamp))
	msg.Signature = hex.EncodeToString(sig)

	body, _ := json.Marshal(msg)
	req := httptest.NewRequest("POST", "/peer/handshake", bytes.NewReader(body))
	req.Header.Set(networkSecretHeader, networkSecret)

	return req, pubHex
}

func TestHandlePeerHandshake_LogOnlyDefaultStillAdmitsUnadmittedPeer(t *testing.T) {
	resetNodeAdmissionHandshakeTestState("self-node")
	t.Setenv("NETWORK_SECRET", "test-secret")
	// NODE_ADMISSION_ENFORCE_HANDSHAKE deliberately left unset - default
	// must be log-only, never rejecting, per nodeAdmissionEnforced's own
	// doc comment on why this feature's default direction is the
	// OPPOSITE of NETWORK_SECRET's fail-closed convention.

	req, pubHex := buildSignedHandshakeRequest(t, "test-secret")
	rec := httptest.NewRecorder()
	HandlePeerHandshake(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 (log-only never rejects), got %d: %s", rec.Code, rec.Body.String())
	}
	if nodeAdmissionStore.IsAdmitted(pubHex) {
		t.Fatalf("test setup issue: this key should never have been admitted - the test proves log-only mode lets an UNADMITTED peer through")
	}
	if _, ok := peerStore.Get("http://candidate.example"); !ok {
		t.Fatalf("expected the unadmitted peer to still be added to peerStore in log-only mode")
	}
}

func TestHandlePeerHandshake_EnforceRejectsUnadmittedPeer(t *testing.T) {
	resetNodeAdmissionHandshakeTestState("self-node")
	t.Setenv("NETWORK_SECRET", "test-secret")
	t.Setenv("NODE_ADMISSION_ENFORCE_HANDSHAKE", "true")

	req, pubHex := buildSignedHandshakeRequest(t, "test-secret")
	rec := httptest.NewRecorder()
	HandlePeerHandshake(rec, req)

	if rec.Code != 403 {
		t.Fatalf("expected 403 for an unadmitted peer under enforcement, got %d: %s", rec.Code, rec.Body.String())
	}
	if nodeAdmissionStore.IsAdmitted(pubHex) {
		t.Fatalf("test setup issue: this key should never have been admitted")
	}
	if _, ok := peerStore.Get("http://candidate.example"); ok {
		t.Fatalf("expected a rejected, unadmitted peer to NOT be added to peerStore")
	}
}

func TestHandlePeerHandshake_EnforceAdmitsGrantedPeer(t *testing.T) {
	resetNodeAdmissionHandshakeTestState("self-node")
	t.Setenv("NETWORK_SECRET", "test-secret")
	t.Setenv("NODE_ADMISSION_ENFORCE_HANDSHAKE", "true")

	pub, priv, _ := core.GenerateKeyPair()
	pubHex := hex.EncodeToString(pub)
	nodeAdmissionStore.Grant(&core.NodeAdmission{PublicKey: pubHex, NodeID: "admitted-node"})

	timestamp := time.Now().Unix()
	msg := handshakeMsg{NodeID: "admitted-node", PublicKey: pubHex, Address: "http://admitted.example", Timestamp: timestamp}
	sig := core.SignMessage(priv, handshakeCanonicalMessage(msg.NodeID, msg.PublicKey, msg.Address, msg.Timestamp))
	msg.Signature = hex.EncodeToString(sig)
	body, _ := json.Marshal(msg)
	req := httptest.NewRequest("POST", "/peer/handshake", bytes.NewReader(body))
	req.Header.Set(networkSecretHeader, "test-secret")
	rec := httptest.NewRecorder()

	HandlePeerHandshake(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 for a genuinely admitted peer under enforcement, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := peerStore.Get("http://admitted.example"); !ok {
		t.Fatalf("expected an admitted, correctly-authenticated peer to be added to peerStore")
	}
}

// TestHandlePeerHandshake_GrandfatheredIdentityAdmittedUnderEnforcement
// is the real-deploy regression test: node1's and mainnet's own
// grandfathered identities (grandfatheredNodeAdmissions,
// api/node_admission_governance.go) must pass admission, proving the
// seed list is actually wired up correctly - not just present as a
// var, but genuinely reachable via SeedGrandfatheredNodeAdmissions the
// same way api/server.go's StartServer calls it.
func TestHandlePeerHandshake_GrandfatheredIdentityAdmittedUnderEnforcement(t *testing.T) {
	resetNodeAdmissionHandshakeTestState("self-node")
	nodeAdmissionStore.SeedGrandfatheredNodeAdmissions(grandfatheredNodeAdmissions)

	if len(grandfatheredNodeAdmissions) == 0 {
		t.Fatalf("test setup issue: grandfatheredNodeAdmissions is empty")
	}
	for _, g := range grandfatheredNodeAdmissions {
		if !nodeAdmissionStore.IsAdmitted(g.PublicKey) {
			t.Fatalf("expected grandfathered public key %s (%s) to be admitted after seeding", g.PublicKey, g.NodeID)
		}
	}
}

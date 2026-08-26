package api

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// resetPeerSecurityTestState gives each test a clean validatorStore/
// peerStore/config - these are real package-level singletons (see
// resetApplyTestState's identical reasoning), and config in particular
// is nil by default in this package's test environment, so every test
// here must set it before exercising anything that reads config.NodeID.
func resetPeerSecurityTestState(selfNodeID string) {
	validatorStore = core.NewValidatorStore()
	peerStore = core.NewPeerStore()
	config = &core.Config{NodeID: selfNodeID}
	nodeIdentity = core.NewNodeIdentity()
}

func signedTestBlock(t *testing.T, priv []byte, validatorAddr, producerNodeID, hash string) core.Block {
	t.Helper()
	block := core.Block{
		Hash:           hash,
		Validator:      validatorAddr,
		ProducerNodeID: producerNodeID,
	}
	block.Signature = core.SignMessage(priv, []byte(block.Hash))
	return block
}

// TestVerifyBlockProducer_RejectsForgedSignatureEvenWithPoisonedPeerStore
// is the regression test for the exact hole this fix closes: before
// it, verifyBlockProducer trusted whatever PublicKey peerStore had on
// file for a NodeID - and peerStore.Add has zero proof-of-possession
// (see HandlePeerHandshake), so an attacker who merely knew a real
// validator's public AuthorizedNodeID could poison peerStore with
// their OWN key under that NodeID and get a self-forged block to
// verify. This test seeds peerStore with exactly that poisoned entry
// and confirms the forged block is still rejected, because
// verifyBlockProducer must now check ValidatorStore's own
// AuthorizedNodePublicKey, never peerStore.
func TestVerifyBlockProducer_RejectsForgedSignatureEvenWithPoisonedPeerStore(t *testing.T) {
	resetPeerSecurityTestState("some-other-node")

	realPub, realPriv, _ := core.GenerateKeyPair()
	attackerPub, attackerPriv, _ := core.GenerateKeyPair()
	_ = attackerPub

	validatorAddr := "validator-address-1"
	validatorStore.Register(validatorAddr, "victim-node", hex.EncodeToString(realPub))

	// Attacker poisons peerStore, claiming the victim's NodeID under
	// their own PublicKey - exactly what an unauthenticated handshake
	// used to allow before signature verification was added.
	peerStore.Add(core.Peer{ID: "victim-node", Address: "http://attacker.example", PublicKey: hex.EncodeToString(attackerPub)})

	forged := signedTestBlock(t, attackerPriv, validatorAddr, "victim-node", "some-hash")

	if verifyBlockProducer(forged) {
		t.Fatal("expected a block forged with the attacker's own key to be REJECTED even though peerStore has a poisoned entry for the claimed NodeID - verifyBlockProducer must anchor to ValidatorStore, not peerStore")
	}

	// Sanity check: the SAME block signed with the real validator's
	// node key must still verify - confirms the rejection above is
	// because of the forged signature, not a broken test setup.
	genuine := signedTestBlock(t, realPriv, validatorAddr, "victim-node", "some-hash")
	if !verifyBlockProducer(genuine) {
		t.Fatal("expected a block genuinely signed by the validator's own authorized node key to verify")
	}
}

func TestVerifyBlockProducer_RejectsWrongNodeID(t *testing.T) {
	resetPeerSecurityTestState("some-other-node")

	pub, priv, _ := core.GenerateKeyPair()
	validatorAddr := "validator-address-1"
	validatorStore.Register(validatorAddr, "authorized-node", hex.EncodeToString(pub))

	block := signedTestBlock(t, priv, validatorAddr, "different-node", "some-hash")
	if verifyBlockProducer(block) {
		t.Fatal("expected a block claiming a NodeID other than the validator's AuthorizedNodeID to be rejected")
	}
}

func TestVerifyBlockProducer_RejectsUnregisteredValidator(t *testing.T) {
	resetPeerSecurityTestState("some-other-node")

	_, priv, _ := core.GenerateKeyPair()
	block := signedTestBlock(t, priv, "never-registered-address", "some-node", "some-hash")
	if verifyBlockProducer(block) {
		t.Fatal("expected a block claiming to come from a validator address with no ValidatorStore entry to be rejected")
	}
}

func TestVerifyBlockProducer_PlaceholderProducerOnlySelfAccepted(t *testing.T) {
	resetPeerSecurityTestState("self-node")

	// The genesis/legacy-"mine" placeholder has no ValidatorStore
	// entry at all - the only legitimate producer is this node itself.
	self := signedTestBlock(t, nodeIdentity.PrivateKey, "creditchain", "self-node", "genesis-hash")
	if !verifyBlockProducer(self) {
		t.Fatal("expected a self-produced placeholder-validator block (signed with this node's own key) to verify")
	}

	// Some other node claiming to have produced a placeholder-validator
	// block must be rejected, even with a technically-valid signature
	// from THEIR OWN key - a placeholder block was never legitimately
	// gossiped from a peer in this codebase's design (every node mints
	// its own genesis locally).
	otherPub, otherPriv, _ := core.GenerateKeyPair()
	_ = otherPub
	other := signedTestBlock(t, otherPriv, "creditchain", "other-node", "genesis-hash")
	if verifyBlockProducer(other) {
		t.Fatal("expected a placeholder-validator block claiming a different NodeID to be rejected")
	}
}

// --- handshake proof-of-possession -----------------------------------

func TestVerifyHandshakeSignature_AcceptsGenuineFreshSignature(t *testing.T) {
	resetPeerSecurityTestState("node-a")

	pub, priv, _ := core.GenerateKeyPair()
	timestamp := time.Now().Unix()
	pubHex := hex.EncodeToString(pub)
	msg := handshakeMsg{
		NodeID:    "node-b",
		PublicKey: pubHex,
		Address:   "http://node-b.example",
		Timestamp: timestamp,
	}
	sig := core.SignMessage(priv, handshakeCanonicalMessage(msg.NodeID, msg.PublicKey, msg.Address, msg.Timestamp))
	msg.Signature = hex.EncodeToString(sig)

	if !verifyHandshakeSignature(msg) {
		t.Fatal("expected a genuinely self-signed, fresh handshake to verify")
	}
}

func TestVerifyHandshakeSignature_RejectsClaimingSomeoneElsesPublicKey(t *testing.T) {
	resetPeerSecurityTestState("node-a")

	victimPub, _, _ := core.GenerateKeyPair()
	_, attackerPriv, _ := core.GenerateKeyPair()

	timestamp := time.Now().Unix()
	msg := handshakeMsg{
		NodeID:    "node-b",
		PublicKey: hex.EncodeToString(victimPub), // claims the VICTIM's public key...
		Address:   "http://attacker.example",
		Timestamp: timestamp,
	}
	// ...but signs with the ATTACKER's own private key, since they
	// don't actually hold the victim's private key.
	sig := core.SignMessage(attackerPriv, handshakeCanonicalMessage(msg.NodeID, msg.PublicKey, msg.Address, msg.Timestamp))
	msg.Signature = hex.EncodeToString(sig)

	if verifyHandshakeSignature(msg) {
		t.Fatal("expected a handshake claiming a PublicKey the signer doesn't actually control to be rejected")
	}
}

func TestVerifyHandshakeSignature_RejectsStaleTimestamp(t *testing.T) {
	resetPeerSecurityTestState("node-a")

	pub, priv, _ := core.GenerateKeyPair()
	staleTimestamp := time.Now().Add(-10 * time.Minute).Unix()
	pubHex := hex.EncodeToString(pub)
	msg := handshakeMsg{
		NodeID:    "node-b",
		PublicKey: pubHex,
		Address:   "http://node-b.example",
		Timestamp: staleTimestamp,
	}
	sig := core.SignMessage(priv, handshakeCanonicalMessage(msg.NodeID, msg.PublicKey, msg.Address, msg.Timestamp))
	msg.Signature = hex.EncodeToString(sig)

	if verifyHandshakeSignature(msg) {
		t.Fatal("expected a handshake signed far outside the acceptable timestamp window to be rejected")
	}
}

func TestVerifyHandshakeSignature_RejectsTamperedAddress(t *testing.T) {
	resetPeerSecurityTestState("node-a")

	pub, priv, _ := core.GenerateKeyPair()
	timestamp := time.Now().Unix()
	pubHex := hex.EncodeToString(pub)
	sig := core.SignMessage(priv, handshakeCanonicalMessage("node-b", pubHex, "http://genuine.example", timestamp))

	// Signature was produced over "http://genuine.example" - swap the
	// address after signing, same tamper-detection property every
	// other signed envelope in this codebase has.
	msg := handshakeMsg{
		NodeID:    "node-b",
		PublicKey: pubHex,
		Address:   "http://tampered.example",
		Timestamp: timestamp,
		Signature: hex.EncodeToString(sig),
	}

	if verifyHandshakeSignature(msg) {
		t.Fatal("expected a handshake with a tampered Address (not matching what was actually signed) to be rejected")
	}
}

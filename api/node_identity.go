package api

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var nodeIdentity core.NodeIdentity
var peerStore = core.NewPeerStore()

type storedIdentity struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

// loadNodeIdentity loads this node's signing keypair from disk, or
// generates and persists a fresh one on first run. Every node needs
// a stable identity across restarts so peers don't have to re-trust
// it from scratch every time it comes back online.
func loadNodeIdentity() {
	path := dataPath("identity.json")

	data, err := os.ReadFile(path)
	if err == nil {
		var stored storedIdentity
		if err := json.Unmarshal(data, &stored); err == nil {
			pub, errPub := hex.DecodeString(stored.PublicKey)
			priv, errPriv := hex.DecodeString(stored.PrivateKey)
			if errPub == nil && errPriv == nil {
				nodeIdentity = core.NodeIdentity{PublicKey: pub, PrivateKey: priv}
				// Tightens permissions on every load, not just at
				// creation - a node upgraded from before this fix
				// (audit finding M-02) already has an identity.json
				// written 0644 (world/group-readable) sitting on disk;
				// this brings an existing file into compliance on the
				// very next restart without requiring a manual chmod
				// step. No-op (and harmless) if it's already 0600.
				_ = os.Chmod(path, 0600)
				return
			}
		}
	}

	nodeIdentity = core.NewNodeIdentity()

	stored := storedIdentity{
		PublicKey:  hex.EncodeToString(nodeIdentity.PublicKey),
		PrivateKey: hex.EncodeToString(nodeIdentity.PrivateKey),
	}

	out, _ := json.MarshalIndent(stored, "", "  ")
	// 0600 (owner read/write only), not 0644 - this file holds the raw
	// Ed25519 PRIVATE key (audit finding M-02). 0644 left it readable
	// by every local account on the host; a low-privilege local
	// compromise, a shared backup, or a support bundle would leak the
	// node's full signing identity, not just its public key.
	_ = os.WriteFile(path, out, 0600)
}

// selfSignedEnvelope builds a SignedEnvelope authenticated by THIS
// node's own identity keypair rather than a wallet's - for
// node-authored transactions with no external HTTP caller to have
// verified in the first place (currently just slash reports; see
// consensus_loop.go's reportMissedBlock / peer.go's reportDoubleSign).
// It exists for exactly the same reason every wallet-originated
// transaction now carries one: so any node applying it can
// independently confirm it was genuinely authored by the address it
// claims, not just trust whichever node produced the block it arrived
// in.
func selfSignedEnvelope(payload []byte) core.SignedEnvelope {
	env := core.SignedEnvelope{
		Address:   reporterAddress(),
		PublicKey: hex.EncodeToString(nodeIdentity.PublicKey),
		Timestamp: time.Now().Unix(),
		Payload:   payload,
	}

	sig := core.SignMessage(nodeIdentity.PrivateKey, env.CanonicalMessage())
	env.Signature = hex.EncodeToString(sig)

	return env
}

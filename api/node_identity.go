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
	_ = os.WriteFile(path, out, 0644)
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

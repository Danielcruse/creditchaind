package api

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// YeneClaimCooldownSeconds/Min/MaxAmount are the user's own explicit
// numbers: a random 1-100 YEN-e grant, once every 5 days per address.
// This stands in for the real Ethereum-side YEN-e bridge, which has no
// live counterpart to bridge from yet (see core.TxYeneClaim's doc
// comment) - a testnet-only representation, not a real bridge.
const (
	YeneClaimCooldownSeconds int64 = 5 * 24 * 3600
	YeneClaimMinAmount             = 1
	YeneClaimMaxAmount             = 100
)

var yeneClaimStore = core.NewYeneClaimStore()

// yeneClaimAmount deterministically derives the claimed amount from
// the transaction's own ID - every node applying the same confirmed
// transaction must compute the identical amount, so this can never be
// real randomness (crypto/rand, time.Now, etc.), only a hash of
// already-agreed-upon transaction data.
func yeneClaimAmount(txID string) uint64 {
	sum := sha256.Sum256([]byte(txID))
	n := binary.BigEndian.Uint64(sum[:8])
	return YeneClaimMinAmount + n%(YeneClaimMaxAmount-YeneClaimMinAmount+1)
}

// HandleYeneClaim queues a testnet YEN-e claim as a real transaction
// (core.TxYeneClaim), applied identically on every node at block-
// confirmation time (applyYeneClaim) - same pending-tx/self-signed-
// amount pattern as HandleFaucet.
func HandleYeneClaim(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	type Request struct{}
	var req Request

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	// Optimistic fast-fail only - the authoritative check is
	// applyYeneClaim's, re-run identically on every node once this
	// transaction actually confirms.
	if !yeneClaimStore.CanClaim(address, time.Now().Unix(), YeneClaimCooldownSeconds) {
		http.Error(w, "you can only claim YEN-e once every 5 days", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxYeneClaim, string(data))

	json.NewEncoder(w).Encode(map[string]any{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleYeneClaimStatus is a public read (no auth) telling the
// frontend whether an address can claim right now, and if not, when.
func HandleYeneClaimStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}

	canClaim := yeneClaimStore.CanClaim(address, time.Now().Unix(), YeneClaimCooldownSeconds)
	json.NewEncoder(w).Encode(map[string]any{
		"canClaim":    canClaim,
		"nextClaimAt": yeneClaimStore.NextClaimAt(address, YeneClaimCooldownSeconds),
	})
}

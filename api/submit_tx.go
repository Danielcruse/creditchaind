package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// HandleSubmitTx is the public, unauthenticated-by-design submission
// endpoint (no NETWORK_SECRET gate - it's meant to be reachable by any
// wallet on the internet), which used to add whatever it decoded
// straight to the mempool with no validation at all - the same
// mempool-DoS gap audit finding M-01 names for the peer-relay path
// (api/peer.go's HandlePeerTx), except worse here since this endpoint
// has no shared-secret gate to begin with. Now runs the identical
// pre-admission checks: a future-timestamp bound (so a submission
// can't dodge core.Mempool.Prune's expiry) and the same verifyTxAuth
// every transaction independently gets re-checked against at apply
// time anyway (api/apply.go) - rejecting an unsigned or malformed
// submission here means it never occupies mempool space, rather than
// only failing once a producer eventually tries to include it.
func HandleSubmitTx(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var tx core.Transaction

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&tx,
		)

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	if mempool.Has(tx.ID) {
		json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
		return
	}

	if !core.ValidateMempoolTimestamp(tx, time.Now().Unix()) {
		fmt.Println("rejected submitted transaction with implausible future timestamp:", tx.ID)
		http.Error(w, "Timestamp too far in the future", http.StatusBadRequest)
		return
	}

	if !verifyTxAuth(tx) {
		fmt.Println("rejected submitted transaction with invalid/mismatched authentication:", tx.ID, tx.Type)
		http.Error(w, "Invalid transaction authentication", http.StatusBadRequest)
		return
	}

	mempool.Add(tx)

	json.NewEncoder(w).Encode(
		map[string]bool{
			"accepted": true,
		},
	)
}
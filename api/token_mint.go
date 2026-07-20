package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type tokenMintData struct {
	Address string `json:"address"`
	Amount  uint64 `json:"amount"`
	// Symbol defaults to CCT - kept optional so every existing
	// caller of this endpoint keeps working unchanged. ETHTEST/
	// USDCTEST/custom tokens route through the same
	// lookupCustomToken chokepoint every other multi-token handler
	// in this codebase already uses.
	Symbol string `json:"symbol"`
}

// HandleTokenMint is an operator-only faucet for testnet seeding -
// real token issuance happens through staking/block rewards
// (api/apply.go), not this endpoint. Gated behind ADMIN_TOKEN so it's
// not an open, unlimited mint for anyone on the internet.
//
// The actual mint is recorded as a TxTokenMint transaction (self-
// signed with this node's own identity, same pattern as
// TxSlashReport - see api/node_identity.go's selfSignedEnvelope)
// rather than mutating the token store directly here - a direct
// mutation only ever takes effect on whichever single node received
// this HTTP request, silently diverging every other node's derived
// state (e.g. a pool add-liquidity transaction that depends on the
// minted balance would succeed on this node but silently no-op
// everywhere else on insufficient balance). Routing through the same
// Transaction/gossip/ApplyBlock pipeline as every other mutation
// replicates it correctly.
func HandleTokenMint(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req tokenMintData
	signedAuth, legacy, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Symbol == "" {
		req.Symbol = "CCT"
	}
	if req.Symbol != "CCT" {
		if _, err := resolveTokenStore(req.Symbol); err != nil {
			http.Error(w, "unknown symbol", http.StatusNotFound)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, legacy, "", 0, core.TxTokenMint, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

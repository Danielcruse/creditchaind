package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type adminTransferData struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount uint64 `json:"amount"`
	// Symbol defaults to CCT, same convention as tokenMintData.
	Symbol string `json:"symbol"`
}

// HandleAdminTransfer is an operator-only recovery tool: moves Amount
// of Symbol that From already holds to To, without From's own
// signature. This exists for exactly one situation - an address
// (e.g. a validator seeded by an early bootstrap script) whose
// private key is permanently unreachable but still holds real
// balance that needs to migrate to a new, key-controlled address so
// it can actually be used (staked, spent, etc.) going forward.
// Deliberately narrower than HandleTokenMint: it can only move value
// an address already has, never create new supply - moving the
// PoSR-affected validators' pre-existing CCT to fresh addresses is
// the motivating case, not a general-purpose admin override.
func HandleAdminTransfer(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req adminTransferData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.From == "" || req.To == "" {
		http.Error(w, "from and to are required", http.StatusBadRequest)
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
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxAdminTransfer, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// applyAdminTransfer is HandleAdminTransfer's apply-side counterpart -
// re-checks nothing beyond what TokenStore.Transfer itself already
// guards (sufficient balance), same "never trust submission-time
// state alone" convention as every other apply function in this
// file. Silently no-ops on insufficient balance or an unresolvable
// symbol, matching TokenStore.Transfer's own established behavior.
func applyAdminTransfer(tx core.Transaction) {
	var data adminTransferData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 || data.From == "" || data.To == "" {
		return
	}

	if data.Symbol == "" || data.Symbol == "CCT" {
		tokenStore.Transfer(data.From, data.To, data.Amount)
		return
	}

	store, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}
	store.Transfer(data.From, data.To, data.Amount)
}

package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// tokenTransferData carries which symbol a TxExchange moves - CCT is
// still the default/implicit case for backward compatibility with
// transactions recorded before this field existed (empty Data), but
// any resolvable symbol (ETHTEST/USDCTEST, REPT, or a developer-
// registered token) can now be sent the same way.
type tokenTransferData struct {
	To     string `json:"to"`
	Amount uint64 `json:"amount"`
	Symbol string `json:"symbol"`
}

func HandleTokenTransfer(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req tokenTransferData

	auth, from, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Symbol == "" {
		req.Symbol = "CCT"
	}

	store, err := resolveTokenStore(req.Symbol)
	if err != nil {
		http.Error(w, "unknown symbol", http.StatusNotFound)
		return
	}

	fromSide := resolveBalanceAddress(from, auth.PublicKey, req.Symbol)
	if store.BalanceOf(fromSide) < req.Amount {
		json.NewEncoder(w).Encode(
			map[string]bool{
				"success": false,
			},
		)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, from, req.To, req.Amount, core.TxExchange, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type TransactionRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount uint64 `json:"amount"`
	Type   string `json:"type"`
	Data   string `json:"data"`
}

func HandleTransaction(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var req TransactionRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(
			w,
			"Invalid JSON",
			http.StatusBadRequest,
		)
		return
	}

	tx := core.NewTransaction(
		req.From,
		req.To,
		req.Amount,
		core.TransactionType(req.Type),
		req.Data,
	)

	mempool.Add(tx)

	json.NewEncoder(w).Encode(tx)
}
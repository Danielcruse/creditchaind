package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
        
)

func HandleMine(
	w http.ResponseWriter,
	r *http.Request,
) {
	if len(mempool.Transactions) == 0 {
		json.NewEncoder(w).Encode(
			map[string]string{
				"message": "No transactions in mempool",
			},
		)
		return
	}

	blockchain.AddBlock(
		"creditchain-validator",
		mempool.Transactions,
	)
      

	mempool.Transactions = []core.Transaction{}

	err := blockchain.Save(
		"blockchain.json",
	)

	if err != nil {
		json.NewEncoder(w).Encode(
			map[string]string{
				"message": "Block mined but save failed",
			},
		)
		return
	}

	json.NewEncoder(w).Encode(
		map[string]string{
			"message": "Block mined and saved successfully",
		},
	)
}
package api

import (
	"encoding/json"
	"net/http"
)

func HandleMetrics(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	txCount := uint64(0)

	for _, block :=
		range blockchain.Blocks {

		txCount +=
			uint64(
				len(
					block.Transactions,
				),
			)
	}

	json.NewEncoder(w).Encode(
		map[string]any{

			"blocks":
				len(
					blockchain.Blocks,
				),

			"transactions":
				txCount,

			"validators":
				0,

			"wallets":
				0,

			"mempool":
				len(
					mempool.Transactions,
				),
		},
	)
}
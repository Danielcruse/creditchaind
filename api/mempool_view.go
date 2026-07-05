package api

import (
	"encoding/json"
	"net/http"
)

func HandleMempool(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		mempool.Transactions,
	)
}
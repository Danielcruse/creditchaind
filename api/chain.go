package api

import (
	"encoding/json"
	"net/http"
)

func HandleChain(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		blockchain.Blocks,
	)
}
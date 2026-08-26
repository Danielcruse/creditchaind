package api

import (
	"encoding/json"
	"net/http"
)

// HandleREPTBalance mirrors HandleTokenBalance/HandleUSDCTestBalance
// but for reptStore - the reputation-progression token.
func HandleREPTBalance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	balance := reptStore.BalanceOf(address)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"balance": balance,
		},
	)
}

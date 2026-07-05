package api

import (
	"encoding/json"
	"net/http"
)

// HandleUSDCTestBalance mirrors HandleTokenBalance but for the
// separate usdcTestStore - borrowed funds, not staking rewards. Kept
// as its own endpoint/store rather than folded into /token/balance so
// the two are never confused with each other.
func HandleUSDCTestBalance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	balance := usdcTestStore.BalanceOf(address)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"balance": balance,
		},
	)
}

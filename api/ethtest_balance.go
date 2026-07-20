package api

import (
	"encoding/json"
	"net/http"
)

// HandleETHTestBalance mirrors HandleUSDCTestBalance/HandleREPTBalance
// but for ethTestStore - the liquid, holdable ETHTEST balance
// (distinct from Wallet.StakedETH, see api/token.go's doc comment).
func HandleETHTestBalance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	balance := ethTestStore.BalanceOf(address)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"balance": balance,
		},
	)
}

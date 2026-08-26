package api

import (
	"encoding/json"
	"net/http"
)

// HandleYenBalance mirrors HandleUSDCTestBalance but for the separate
// yenStore - YEN-e is the stable settlement currency (lending,
// marketplace payments, transfers), kept as its own ledger from CCT
// (network utility/staking/governance) per the approved tokenomics
// split.
func HandleYenBalance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	balance := yenStore.BalanceOf(address)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"balance": balance,
		},
	)
}

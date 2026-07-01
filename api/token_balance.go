package api

import (
	"encoding/json"
	"net/http"
)

func HandleTokenBalance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address :=
		r.URL.Query().
			Get("address")

	balance :=
		tokenStore.
			BalanceOf(
				address,
			)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"balance":
				balance,
		},
	)
}
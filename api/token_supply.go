package api

import (
	"encoding/json"
	"net/http"
)

func HandleTokenSupply(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"supply":
				tokenStore.Token.
					TotalSupply,
		},
	)
}
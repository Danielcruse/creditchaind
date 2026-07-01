package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var creditHistory =
	core.NewCreditHistory()

func HandleCreditHistory(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address :=
		r.URL.Query().Get(
			"address",
		)

	json.NewEncoder(w).Encode(
		creditHistory.Get(
			address,
		),
	)
}
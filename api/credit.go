package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var creditStore =
	core.NewCreditStore()

func HandleCredit(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address :=
		r.URL.Query().Get(
			"address",
		)

	profile :=
		creditStore.Get(
			address,
		)

	core.CalculateScore(
		profile,
	)

	json.NewEncoder(w).Encode(
		profile,
	)
}
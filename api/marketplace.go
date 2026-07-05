package api

import (
	"encoding/json"
	"net/http"
       "github.com/creditchain/creditchaind/core"
)

func HandleMarketplace(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	loans := []*core.Loan{}

	for _, loan :=
		range loanStore.Loans {

		if loan.Status ==
			"PENDING" {

			loans =
				append(
					loans,
					loan,
				)
		}
	}

	json.NewEncoder(w).Encode(
		loans,
	)
}
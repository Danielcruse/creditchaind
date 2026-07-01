package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var lenderStore =
	core.NewLenderStore()

func HandleLenderStats(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address :=
		r.URL.Query().Get(
			"address",
		)

	stats :=
		lenderStore.Get(
			address,
		)

	json.NewEncoder(w).Encode(
		stats,
	)
}
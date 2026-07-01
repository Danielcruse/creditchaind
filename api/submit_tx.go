package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandleSubmitTx(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var tx core.Transaction

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&tx,
		)

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	mempool.Add(tx)

	json.NewEncoder(w).Encode(
		map[string]bool{
			"accepted": true,
		},
	)
}
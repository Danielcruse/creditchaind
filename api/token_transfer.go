package api

import (
	"encoding/json"
	"net/http"
)

func HandleTokenTransfer(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		From   string
		To     string
		Amount uint64
	}

	var req Request

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&req,
		)

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	ok :=
		tokenStore.Transfer(
			req.From,
			req.To,
			req.Amount,
		)

	json.NewEncoder(w).Encode(
		map[string]bool{
			"success":
				ok,
		},
	)
}
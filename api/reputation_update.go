package api

import (
	"encoding/json"
	"net/http"
)

func HandleReputationUpdate(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		Address string
		Amount  uint64
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

	creditStore.UpdateReputation(
		req.Address,
		req.Amount,
	)

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "updated",
		},
	)
}
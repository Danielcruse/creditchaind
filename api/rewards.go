package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandlePoolRewards(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		ValidatorID string

		Reward uint64
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

	for i :=
		range validatorPools {

		if validatorPools[i].
			ValidatorID ==
			req.ValidatorID {

			rewards :=
				core.DistributeRewards(
					&validatorPools[i],
					req.Reward,
				)

			json.NewEncoder(w).Encode(
				rewards,
			)

			return
		}
	}

	http.Error(
		w,
		"pool not found",
		http.StatusNotFound,
	)
}
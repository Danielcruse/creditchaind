package api

import (
	"encoding/json"
	"net/http"
)

func HandleStake(
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

	stakingStore.StakeETH(
		req.Address,
		req.Amount,
	)

	reward :=
		stakingStore.Reward(
			req.Address,
		)

	tokenStore.Mint(
		req.Address,
		reward,
	)

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"reward": reward,
		},
	)
}
package api

import (
	"encoding/json"
	"net/http"
"github.com/creditchain/creditchaind/core"
)
var stakingStore=core.NewStakingStore()

type StakingResponse struct {
	Address     string `json:"address"`
	StakedETH   uint64 `json:"stakedETH"`
	OETHBalance uint64 `json:"oethBalance"`
	Rewards     uint64 `json:"rewards"`
	VotingPower uint64 `json:"votingPower"`
}

func HandleStaking(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	if address == "" {
		w.WriteHeader(http.StatusBadRequest)

		json.NewEncoder(w).Encode(
			map[string]string{
				"error": "missing address",
			},
		)

		return
	}

	/*
		TODO:
		Load real staking data from wallet storage
		or staking manager when exposed by the node.
	*/

	resp := StakingResponse{
		Address:     address,
		StakedETH:   0,
		OETHBalance: 0,
		Rewards:     0,
		VotingPower: 0,
	}

	json.NewEncoder(w).Encode(resp)
}
package api

import (
	"encoding/json"
	"net/http"
)

type WalletResponse struct {
	Address      string `json:"address"`
	CCTBalance   uint64 `json:"cctBalance"`
	OETHBalance  uint64 `json:"oethBalance"`
	StakedETH    uint64 `json:"stakedETH"`
	Rewards      uint64 `json:"rewards"`
	Reputation   uint64 `json:"reputation"`
	CreditScore  uint64 `json:"creditScore"`
	VotingPower  uint64 `json:"votingPower"`
	NodeID       string `json:"nodeId"`
}

func HandleWallet(
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
		Load real wallet data from wallet storage/state.
		For now we return placeholders until
		the wallet store endpoint exists.
	*/

	resp := WalletResponse{
		Address:     address,
		CCTBalance:  0,
		OETHBalance: 0,
		StakedETH:   0,
		Rewards:     0,
		Reputation:  50,
		CreditScore: 50,
		VotingPower: 0,
		NodeID:      "",
	}

	json.NewEncoder(w).Encode(resp)
}
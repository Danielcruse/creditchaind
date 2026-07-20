package api

import (
	"encoding/json"
	"net/http"
)

type NetworkInfo struct {
	NetworkName       string `json:"networkName"`
	BlockHeight       int    `json:"blockHeight"`
	LatestBlockHash   string `json:"latestBlockHash"`
	PeerCount         int    `json:"peerCount"`
	ActiveValidators  int    `json:"activeValidators"`
	TPS               int    `json:"tps"`
	GasPrice          uint64 `json:"gasPrice"`
	TotalSupply       uint64 `json:"totalSupply"`
	CirculatingSupply uint64 `json:"circulatingSupply"`
	Status            string `json:"status"`
}

func HandleNetwork(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	info := NetworkInfo{
		NetworkName:       "OUTERNET",
		BlockHeight:       len(blockchain.Blocks),
		LatestBlockHash:   latestHash(),
		PeerCount:         len(peerStore.List()),
		ActiveValidators:  countActiveValidators(),
		TPS:               0, // TODO: calculate from recent blocks
		GasPrice:          1, // TODO: dynamic gas engine
		TotalSupply:       tokenStore.Token.TotalSupply,
		CirculatingSupply: tokenStore.Token.TotalSupply,
		Status:            "LIVE",
	}

	json.NewEncoder(w).Encode(info)
}

// countActiveValidators is shared by HandleNetwork and
// HandleProtocolStats - a single real computation over
// validatorStore.List(), not duplicated logic.
func countActiveValidators() int {
	count := 0
	for _, v := range validatorStore.List() {
		if v.Active {
			count++
		}
	}
	return count
}

func latestHash() string {
	if blockchain == nil {
		return ""
	}

	if len(blockchain.Blocks) == 0 {
		return ""
	}

	return blockchain.
		Blocks[len(
		blockchain.Blocks,
	)-1].
		Hash
}

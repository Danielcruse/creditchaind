package api

import (
	"encoding/json"
	"net/http"
)

func HandleNode(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		map[string]any{
			"nodeId": config.NodeID,
			"version": config.Version,
			"network": config.Network,
			"chainId": config.ChainID,
			"blockHeight": len(
				blockchain.Blocks,
			),
		},
	)
}